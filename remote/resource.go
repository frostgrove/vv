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
// gateway, decorates like any other repository, and stands in for a local one
// in a test. It deliberately does not satisfy crud.Core: that has Tx, and a
// transaction does not cross a stateless call, so the honest thing is not to
// offer one.
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

	"github.com/frostgrove/vv/crud"
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
	return decode[crud.PaginatedResponse[M]](raw, "a page")
}

// GetAll returns every matching row.
//
// Unpaged is set on the document rather than left to the caller, and the
// service still applies its own maximum page size — crud.Options.Resolved does
// the same thing locally, so a repository that caps a page caps this too.
func (r *Resource[M, ID, U]) GetAll(ctx context.Context, opts ...crud.Option) ([]M, error) {
	req, err := ToRequest(opts...)
	if err != nil {
		return nil, err
	}
	req.Unpaged = true
	raw, err := r.tr.Do(ctx, Call{Method: MethodList, Query: req})
	if err != nil {
		return nil, err
	}
	page, err := decode[crud.PaginatedResponse[M]](raw, "a page")
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// GetByID returns one row, or an error matching crud.ErrNotFound.
func (r *Resource[M, ID, U]) GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	req, err := ToRequest(opts...)
	if err != nil {
		return zero, err
	}
	// The same narrowing the service applies on the way in. Doing it here too
	// keeps a filter or a page number out of a request that names one row,
	// where they mean nothing and would only be dropped further along.
	port.NarrowForEntity(req)
	raw, err := r.tr.Do(ctx, Call{Method: MethodGet, ID: port.FormatID(id), Query: req})
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
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
		Count int64 `json:"count"`
	}](raw, "a count")
	return body.Count, err
}

// ---------------------------------------------------------------------------
// writes

// Save creates when the primary key is unset and replaces the named row
// otherwise, and refreshes the model in place with what the service answered.
//
// That is crud.Core.Save's own rule read onto two routes rather than a choice
// made here: an unset key is a POST because the service is the thing that
// generates one, and a set key is a PUT because the caller named the row.
func (r *Resource[M, ID, U]) Save(ctx context.Context, m *M) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("remote: encoding the model: %w", err)
	}
	has, err := r.meta.HasID(m)
	if err != nil {
		return err
	}

	call := Call{Method: MethodCreate, Body: body}
	if has {
		key, err := keyOf[ID](r.meta, m)
		if err != nil {
			return err
		}
		call = Call{Method: MethodReplace, ID: key, Body: body}
	}

	raw, err := r.tr.Do(ctx, call)
	if err != nil {
		return err
	}
	saved, err := decode[M](raw, "an entity")
	if err != nil {
		return err
	}
	*m = saved
	return nil
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
		Deleted int64 `json:"deleted"`
	}](raw, "a delete count")
	return body.Deleted, err
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
