// Package remote is a repository that is not in this process.
//
// It is the consuming half of the framework. One service declares a CRUD API
// with crudnet, crudfiber, crudgin or crudgrpc; another holds a
// [Resource] over the same model and calls it with the methods it would use on
// a repository of its own:
//
//	articles := remote.New[Article, int64, ArticleInput](
//	    remotehttp.Transport("https://content.internal/articles"))
//
//	page, err := articles.Get(ctx,
//	    crud.Where(crud.Eq("Status", "draft")),
//	    crud.OrderBy(crud.Desc("CreatedAt")),
//	    crud.Limit(20))
//
// A [Resource] satisfies port.Repository, so it mounts on a binding as a
// gateway and stands in for a local repository at that seam. It deliberately
// does not satisfy crud.Core: that has Tx, and a transaction does not cross a
// stateless call, so Core-only decorators such as security.Gate cannot wrap it.
// The owning service remains the enforcement point for those policies.
//
// What the network costs is written down rather than discovered. Two options
// cannot be honoured and are named in [ToRequest]; three are refused outright,
// because an option that changes which rows come back must not go missing
// quietly. Everything else — the filter, the sort, the projection, the
// preloads, the paging, the cursor — asks the same question it would ask a
// repository in this process.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/port"
)

// A Resource is one model's API on another service.
type Resource[M any, ID comparable, U any] struct {
	tr   Transport
	meta *crud.Meta
}

// New binds a model to a transport. It panics when the model cannot be
// described or its key does not match ID, which is the same start-up failure
// sqlrepo.Define answers with and for the same reason: both are program errors
// that no request can recover from.
func New[M any, ID comparable, U any](tr Transport) *Resource[M, ID, U] {
	r, err := TryNew[M, ID, U](tr)
	if err != nil {
		panic(err)
	}
	return r
}

// TryNew is [New] without the panic.
func TryNew[M any, ID comparable, U any](tr Transport) (*Resource[M, ID, U], error) {
	if tr == nil {
		return nil, fmt.Errorf("remote: a resource needs a transport")
	}
	// The table name means nothing here — no statement is built — but the
	// schema does: it is what resolves a field path, tells a set key from an
	// unset one, and refuses a model this library cannot describe. A remote
	// resource that would not work locally should fail at the same moment.
	meta, err := crud.NewMeta[M]("")
	if err != nil {
		return nil, err
	}
	var id ID
	if err := meta.CheckID(reflect.TypeOf(&id).Elem()); err != nil {
		return nil, err
	}
	var dto U
	if err := checkPatchable(reflect.TypeOf(&dto).Elem()); err != nil {
		return nil, err
	}
	return &Resource[M, ID, U]{tr: tr, meta: meta}, nil
}

var _ port.Repository[struct{}, int, struct{}] = (*Resource[struct{}, int, struct{}])(nil)

// Meta describes the bound model. It is built by reflection over M and reaches
// no database, which is what lets a remote resource be handed straight back to
// a binding and re-exposed.
func (r *Resource[M, ID, U]) Meta() *crud.Meta { return r.meta }

// ---------------------------------------------------------------------------
// reads

// Get returns a page of rows.
func (r *Resource[M, ID, U]) Get(ctx context.Context, opts ...crud.Option) (crud.PaginatedResponse[M], error) {
	var zero crud.PaginatedResponse[M]
	req, err := ToRequest(opts...)
	if err != nil {
		return zero, err
	}
	raw, err := r.tr.Do(ctx, Call{Method: MethodList, Query: req})
	if err != nil {
		return zero, err
	}
	return decodePage[M](raw)
}

// GetAll returns every matching row.
//
// An all-rows request walks remote pages until the service says there are no
// more. It asks for a stable primary-key sort when the caller did not supply
// one, then changes from the first offset page to its cursor edge. A far-side
// maximum page size and offset budget are therefore chunking controls, not a
// silently truncated export. Explicit page, limit or offset without Unpaged
// remain the caller's request and return that subset; Unpaged plus Offset
// returns the whole suffix after the offset.
func (r *Resource[M, ID, U]) GetAll(ctx context.Context, opts ...crud.Option) ([]M, error) {
	req, err := ToRequest(opts...)
	if err != nil {
		return nil, err
	}
	// Unpaged wins over page/limit in crud.Options, so it remains an all-rows
	// request even when a caller accidentally supplied both. Offset is the one
	// control that intentionally selects a suffix; page/limit select one page.
	if (req.Offset != 0 && !req.Unpaged) || (!req.Unpaged && (req.Page != 0 || req.Limit != 0)) {
		page, err := r.list(ctx, req)
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	return r.allPages(ctx, req)
}

// First asks the remote list endpoint for one matching row. The remote protocol
// has no separate first route, so its list shape is normalised exactly as the
// local repository normalises Get's paging controls.
func (r *Resource[M, ID, U]) First(ctx context.Context, opts ...crud.Option) (M, error) {
	var zero M
	req, err := ToRequest(opts...)
	if err != nil {
		return zero, err
	}
	q := *req
	q.Page, q.Limit, q.Offset = 0, 1, 0
	q.After, q.Before = "", ""
	q.Unpaged, q.SkipTotal = false, true
	page, err := r.list(ctx, &q)
	if err != nil {
		return zero, err
	}
	if len(page.Items) == 0 {
		return zero, crud.ErrNotFound
	}
	return page.Items[0], nil
}

func (r *Resource[M, ID, U]) list(ctx context.Context, req *query.Request) (crud.PaginatedResponse[M], error) {
	raw, err := r.tr.Do(ctx, Call{Method: MethodList, Query: req})
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	return decodePage[M](raw)
}

// allPages preserves the query document while changing only the paging edge.
// A list response is the only cross-process read primitive, so walking it is
// the one way to keep GetAll honest when the far repository caps a page.
func (r *Resource[M, ID, U]) allPages(ctx context.Context, req *query.Request) ([]M, error) {
	q := *req
	q.Unpaged, q.SkipTotal = false, false
	q.Page, q.Limit = 0, 0
	backward := q.Before != ""
	cursoring := q.After != "" || backward
	offsetWalk := q.Offset != 0 && !cursoring
	skip := 0
	if req.Unpaged && q.Offset > 0 && !cursoring {
		// The remote endpoint is allowed to reject a deep offset before it
		// can return a cursor edge. Start at zero and discard the prefix here
		// instead, preserving GetAll(Unpaged, Offset(k))'s whole-suffix
		// contract without asking the public endpoint to widen MaxOffset.
		skip, q.Offset, offsetWalk = q.Offset, 0, false
	}
	if len(q.Sort) == 0 && !(q.Distinct && len(q.Select) > 0 && !selectsPrimaryKey(r.meta, q.Select)) {
		// A cursor names values in a sort tuple. The repository default is not
		// part of the wire contract, so name the universal stable tuple rather
		// than relying on it when this walk switches to a cursor after page one.
		q.Sort = query.Sorts{{Field: r.meta.PK.Name}}
	}

	seenCursors := map[string]struct{}{}
	if cursoring {
		if backward {
			seenCursors[q.Before] = struct{}{}
		} else {
			seenCursors[q.After] = struct{}{}
		}
	}
	var items []M
	var backwardPages [][]M
	received := 0
	var expectedTotal int64 = -1
	var last crud.PaginatedResponse[M]
	for {
		page, err := r.list(ctx, &q)
		if err != nil {
			return nil, err
		}
		last = page
		pageItems := page.Items
		if skip > 0 {
			dropped := min(skip, len(pageItems))
			pageItems, skip = pageItems[dropped:], skip-dropped
		}
		received += len(pageItems)
		if backward {
			// Prepending each page copies everything seen so far. Keep pages in
			// transport order and concatenate them once, backwards, after the
			// walk; that preserves sort order in linear time.
			backwardPages = append(backwardPages, pageItems)
		} else {
			items = append(items, pageItems...)
		}

		if cursoring {
			next := page.NextCursor
			more := page.HasNext
			if backward {
				next = page.PrevCursor
				more = page.HasPrev
			}
			if len(page.Items) == 0 {
				// A cursor edge is made from a row. An empty cursor page cannot
				// honestly lead anywhere; when it is terminal it is the proof
				// that the preceding non-empty edge exhausted the walk.
				if next != "" || more {
					return nil, &PartialResultError{Received: received, Total: page.Total}
				}
				break
			}
			if next == "" {
				if !more {
					break
				}
				return nil, &PartialResultError{Received: received, Total: page.Total}
			}
			if _, repeated := seenCursors[next]; repeated {
				return nil, &PartialResultError{Received: received, Total: page.Total}
			}
			seenCursors[next] = struct{}{}
			if backward {
				q.Before, q.After = next, ""
			} else {
				q.After, q.Before = next, ""
			}
			continue
		}

		if !offsetWalk && ((q.Page == 0 && page.Page != 1) || (q.Page > 0 && page.Page != q.Page)) {
			return nil, &PartialResultError{Received: received, Total: page.Total}
		}
		if page.NextCursor != "" {
			if len(page.Items) == 0 {
				return nil, &PartialResultError{Received: received, Total: page.Total}
			}
			// The first offset response has the one exact count in this walk.
			// Cursor responses deliberately omit a whole-set COUNT, so compare
			// the completed cursor walk with this value at the end instead.
			expectedTotal = page.Total
			if offsetWalk || (req.Unpaged && req.Offset > 0) {
				expectedTotal -= int64(req.Offset)
				if expectedTotal < 0 {
					expectedTotal = 0
				}
			}
			cursoring = true
			seenCursors[page.NextCursor] = struct{}{}
			q.After, q.Before, q.Offset = page.NextCursor, "", 0
			continue
		}
		if !page.HasNext {
			break
		}
		if len(page.Items) == 0 {
			return nil, &PartialResultError{Received: received, Total: page.Total}
		}
		if page.Page < 1 || page.Limit < 1 || page.TotalPages <= page.Page {
			return nil, &PartialResultError{Received: received, Total: page.Total}
		}
		if offsetWalk {
			q.Offset = req.Offset + received
			continue
		}
		q.Page, q.Limit = page.Page+1, page.Limit
	}
	if backward {
		items = make([]M, 0, received)
		for i := len(backwardPages) - 1; i >= 0; i-- {
			items = append(items, backwardPages[i]...)
		}
	}
	// sqlrepo deliberately omits a whole-result COUNT on cursor pages: their
	// edges, not Total, are the completion proof. When an offset first page
	// supplied a total before the walk moved to cursors, it still gives us a
	// useful final consistency check.
	total := last.Total
	if expectedTotal >= 0 {
		total = expectedTotal
	} else if offsetWalk || (req.Unpaged && req.Offset > 0) {
		total -= int64(req.Offset)
		if total < 0 {
			total = 0
		}
	}
	if (!cursoring || expectedTotal >= 0) && int64(received) != total {
		return nil, &PartialResultError{Received: received, Total: total}
	}
	return items, nil
}

func selectsPrimaryKey(m *crud.Meta, fields query.Strings) bool {
	for _, name := range fields {
		if f := m.Field(name); f != nil && f == m.PK {
			return true
		}
	}
	return false
}

// GetByID returns one row, or an error matching crud.ErrNotFound.
func (r *Resource[M, ID, U]) GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	req, err := ToRequest(opts...)
	if err != nil {
		return zero, err
	}
	// The same narrowing the service applies on the way in: paging and ordering
	// cannot affect a primary-key lookup, but a predicate can still prove the
	// named row is outside the caller's scope.
	port.NarrowForEntity(req)
	if entityNeedsList(req) {
		return r.getByIDThroughList(ctx, id, req)
	}
	raw, err := r.tr.Do(ctx, Call{Method: MethodGet, ID: port.FormatID(id), Query: req})
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
}

// entityNeedsList selects the document-shaped List route when the keyed GET
// route cannot carry the caller's narrowing. HTTP's entity route deliberately
// carries only projection and plain preload paths; using List preserves a root
// filter and a narrowed or capped preload rather than silently flattening any
// of them.
func entityNeedsList(req *query.Request) bool {
	if req == nil {
		return false
	}
	if !req.Filter.IsZero() {
		return true
	}
	for _, pre := range req.Preload {
		if !pre.Filter.IsZero() || len(pre.Sort) > 0 || pre.MaxRows > 0 {
			return true
		}
	}
	return false
}

func (r *Resource[M, ID, U]) getByIDThroughList(ctx context.Context, id ID, req *query.Request) (M, error) {
	var zero M
	// The List fallback travels through the query document. Spell the key as
	// text, as keyed calls already do, so an int64 at magnitude 2^53 and beyond stays out of
	// protobuf Struct before the peer coerces it back to the primary-key type.
	idFilter, err := crud.MarshalPredicate(crud.Eq(r.meta.PK.Name, port.FormatID(id)))
	if err != nil {
		return zero, &OptionError{
			Option: "GetByID filter",
			Reason: "the primary-key value cannot be encoded for the List fallback: " + err.Error(),
		}
	}
	if req.Filter.IsZero() {
		req.Filter = query.RawFilter(string(idFilter))
	} else {
		filter, err := req.Filter.MarshalJSON()
		if err != nil {
			return zero, err
		}
		req.Filter = query.RawFilter(`{"and":[` + string(filter) + `,` + string(idFilter) + `]}`)
	}
	page, err := r.list(ctx, req)
	if err != nil {
		return zero, err
	}
	switch len(page.Items) {
	case 0:
		return zero, crud.ErrNotFound
	case 1:
		return page.Items[0], nil
	default:
		return zero, fmt.Errorf("remote: primary-key List fallback returned %d rows", len(page.Items))
	}
}

// Count reports how many rows match.
func (r *Resource[M, ID, U]) Count(ctx context.Context, opts ...crud.Option) (int64, error) {
	req, err := ToRequest(opts...)
	if err != nil {
		return 0, err
	}
	port.NarrowForCount(req)
	raw, err := r.tr.Do(ctx, Call{Method: MethodCount, Query: req})
	if err != nil {
		return 0, err
	}
	body, err := decode[struct {
		Count exactInt64 `json:"count"`
	}](raw, "a count")
	return int64(body.Count), err
}

// ---------------------------------------------------------------------------
// writes

// Save creates when the primary key is unset and replaces the named row
// otherwise. It returns what the service stored and never mutates m.
//
// That is crud.Core.Save's own rule read onto two routes rather than a choice
// made here: an unset key is a POST because the service is the thing that
// generates one, and a set key is a PUT because the caller named the row.
func (r *Resource[M, ID, U]) Save(ctx context.Context, m *M) (M, error) {
	var zero M
	call, err := r.saveCall(m)
	if err != nil {
		return zero, err
	}
	raw, err := r.tr.Do(ctx, call)
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
}

// SaveOnly sends the same create-or-replace command but intentionally discards
// the entity response. A remote API still produces that response for its own
// default create/replace contract; this method simply never decodes or applies
// it to the caller's model.
func (r *Resource[M, ID, U]) SaveOnly(ctx context.Context, m *M) error {
	call, err := r.saveCall(m)
	if err != nil {
		return err
	}
	_, err = r.tr.Do(ctx, call)
	return err
}

func (r *Resource[M, ID, U]) saveCall(m *M) (Call, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return Call{}, fmt.Errorf("remote: encoding the model: %w", err)
	}
	has, err := r.meta.HasID(m)
	if err != nil {
		return Call{}, err
	}

	call := Call{Method: MethodCreate, Body: body}
	if has {
		key, err := keyOf[ID](r.meta, m)
		if err != nil {
			return Call{}, err
		}
		call = Call{Method: MethodReplace, ID: key, Body: body}
	}
	return call, nil
}

// Update applies a partial update and returns the refreshed row.
//
// Options are refused rather than ignored. Locally they narrow both the load
// and the UPDATE's own WHERE, which is how a decorator keeps a row that moved
// out of scope from being written anyway; there is no route that carries them,
// so accepting one would be accepting a promise this cannot keep.
func (r *Resource[M, ID, U]) Update(ctx context.Context, id ID, dto U, opts ...crud.Option) (M, error) {
	var zero M
	if err := refuseOptions("Update", opts); err != nil {
		return zero, err
	}
	body, err := json.Marshal(dto)
	if err != nil {
		return zero, fmt.Errorf("remote: encoding the patch: %w", err)
	}
	raw, err := r.tr.Do(ctx, Call{Method: MethodUpdate, ID: port.FormatID(id), Body: body})
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
}

// Delete removes rows by key and reports how many went away.
//
// One key goes to the single-row route and a set to the bulk one, which is the
// difference the two routes exist for: deleting one row that is not there is
// crud.ErrNotFound and deleting a set that matched nothing is a truthful zero.
//
// The 404 is turned back into a zero here, and that is deliberate. This is a
// repository, and a repository answers "no rows went away"; port's own service
// is what turns that into crud.ErrNotFound, and it would do it twice if this
// returned the error as well — harmless for a direct caller, wrong for a
// bulk delete that routed through here.
func (r *Resource[M, ID, U]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	switch len(ids) {
	case 0:
		// Asking a service to delete nothing is a round trip with no statement
		// at the end of it. DefaultService.DeleteMany makes the same call.
		return 0, nil
	case 1:
		raw, err := r.tr.Do(ctx, Call{Method: MethodDelete, ID: port.FormatID(ids[0])})
		if err != nil {
			if errors.Is(err, crud.ErrNotFound) {
				return 0, nil
			}
			return 0, err
		}
		return deletedCount(raw)
	}

	keys, err := json.Marshal(ids)
	if err != nil {
		return 0, fmt.Errorf("remote: encoding the keys: %w", err)
	}
	raw, err := r.tr.Do(ctx, Call{Method: MethodBulkDelete, IDs: keys})
	if err != nil {
		return 0, err
	}
	return deletedCount(raw)
}

func deletedCount(raw json.RawMessage) (int64, error) {
	body, err := decode[struct {
		Deleted exactInt64 `json:"deleted"`
	}](raw, "a delete count")
	return int64(body.Deleted), err
}

// exactInt64 accepts both the conventional JSON number and crudgrpc's decimal
// string representation for an int64 outside vv's safe Struct integer policy
// (|n| < 2^53).
type exactInt64 int64

func (n *exactInt64) UnmarshalJSON(raw []byte) error {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		value, err := strconv.ParseInt(asString, 10, 64)
		if err != nil {
			return fmt.Errorf("exact integer %q: %w", asString, err)
		}
		*n = exactInt64(value)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return fmt.Errorf("exact integer %s: %w", number, err)
	}
	*n = exactInt64(value)
	return nil
}

func decodePage[M any](raw json.RawMessage) (crud.PaginatedResponse[M], error) {
	type page struct {
		Items      []M        `json:"items"`
		Page       int        `json:"page"`
		Limit      int        `json:"limit"`
		Total      exactInt64 `json:"total"`
		TotalPages exactInt64 `json:"totalPages"`
		HasNext    bool       `json:"hasNext"`
		HasPrev    bool       `json:"hasPrev"`
		NextCursor string     `json:"nextCursor"`
		PrevCursor string     `json:"prevCursor"`
	}
	body, err := decode[page](raw, "a page")
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	totalPages, err := exactIntToInt(body.TotalPages)
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	return crud.PaginatedResponse[M]{
		Items: body.Items, Page: body.Page, Limit: body.Limit, Total: int64(body.Total),
		TotalPages: totalPages, HasNext: body.HasNext, HasPrev: body.HasPrev,
		NextCursor: body.NextCursor, PrevCursor: body.PrevCursor,
	}, nil
}

func exactIntToInt(n exactInt64) (int, error) {
	max := int64(^uint(0) >> 1)
	min := -max - 1
	if int64(n) < min || int64(n) > max {
		return 0, fmt.Errorf("exact integer %d does not fit Go int", n)
	}
	return int(n), nil
}

// ---------------------------------------------------------------------------

// decode reads a response document.
//
// A service that answers something this cannot read is an infrastructure
// failure, not a classified one, so the error carries no fault: port.KindOf
// reads an unrecognised error as internal, which is the status a gateway should
// pass on. What it must not do is arrive as a zero value and a nil error.
func decode[T any](raw json.RawMessage, what string) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, fmt.Errorf("remote: the service answered nothing where %s was expected", what)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("remote: the service did not answer %s: %w", what, err)
	}
	return v, nil
}

// refuseOptions rejects any option on a route that carries none.
func refuseOptions(method string, opts []crud.Option) error {
	for _, o := range opts {
		if o != nil {
			return &OptionError{"an option on " + method,
				"the route carries no query document, so nothing would be narrowed"}
		}
	}
	return nil
}

// keyOf reads a model's primary key as the text a route carries.
//
// The three arms are Schema.CheckID's three: the key type itself, a pointer to
// it, or a crud.Opt of it. Formatting the raw value instead would send a
// pointer as its address, which the other end would read as a key nobody has.
func keyOf[ID comparable](meta *crud.Meta, m any) (string, error) {
	v, err := meta.ID(m)
	if err != nil {
		return "", err
	}
	switch id := v.(type) {
	case ID:
		return port.FormatID(id), nil
	case *ID:
		if id == nil {
			return "", fmt.Errorf("remote: the primary key of %s is a nil pointer", meta.Name)
		}
		return port.FormatID(*id), nil
	case crud.Opt[ID]:
		if set, ok := id.Get(); ok {
			return port.FormatID(set), nil
		}
		return "", fmt.Errorf("remote: the primary key of %s holds no value", meta.Name)
	}
	return "", fmt.Errorf("remote: the primary key of %s is %T, which is not the key type", meta.Name, v)
}
