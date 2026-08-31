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

type Resource[M any, ID comparable, U any] struct {
	tr   Transport
	meta *crud.Meta
}

func New[M any, ID comparable, U any](tr Transport) *Resource[M, ID, U] {
	r, err := TryNew[M, ID, U](tr)
	if err != nil {
		panic(err)
	}
	return r
}

func TryNew[M any, ID comparable, U any](tr Transport) (*Resource[M, ID, U], error) {
	if tr == nil {
		return nil, fmt.Errorf("remote: a resource needs a transport")
	}

	meta, err := crud.NewMeta[M]("")
	if err != nil {
		return nil, err
	}
	var id ID
	if err := meta.CheckID(reflect.TypeOf(&id).Elem()); err != nil {
		return nil, err
	}
	var dataTransferObject U
	if err := checkPatchable(reflect.TypeOf(&dataTransferObject).Elem()); err != nil {
		return nil, err
	}
	return &Resource[M, ID, U]{tr: tr, meta: meta}, nil
}

var _ port.Repository[struct{}, int, struct{}] = (*Resource[struct{}, int, struct{}])(nil)

func (this *Resource[M, ID, U]) Meta() *crud.Meta { return this.meta }

func (this *Resource[M, ID, U]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	var zero crud.PaginatedResponse[M]
	request, err := ToRequest(options...)
	if err != nil {
		return zero, err
	}
	raw, err := this.tr.Do(ctx, &Call{Method: MethodList, Query: request})
	if err != nil {
		return zero, err
	}
	return decodePage[M](raw)
}

func (this *Resource[M, ID, U]) GetAll(ctx context.Context, options ...crud.Option) ([]M, error) {
	request, err := ToRequest(options...)
	if err != nil {
		return nil, err
	}

	if (request.Offset != 0 && !request.Unpaged) || (!request.Unpaged && (request.Page != 0 || request.Limit != 0)) {
		page, err := this.list(ctx, request)
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	return this.allPages(ctx, request)
}

func (this *Resource[M, ID, U]) First(ctx context.Context, options ...crud.Option) (M, error) {
	var zero M
	request, err := ToRequest(options...)
	if err != nil {
		return zero, err
	}
	q := *request
	q.Page, q.Limit, q.Offset = 0, 1, 0
	q.After, q.Before = "", ""
	q.Unpaged, q.SkipTotal = false, true
	page, err := this.list(ctx, &q)
	if err != nil {
		return zero, err
	}
	if len(page.Items) == 0 {
		return zero, crud.ErrNotFound
	}
	return page.Items[0], nil
}

func (this *Resource[M, ID, U]) list(ctx context.Context, request *query.Request) (crud.PaginatedResponse[M], error) {
	raw, err := this.tr.Do(ctx, &Call{Method: MethodList, Query: request})
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	return decodePage[M](raw)
}

func (this *Resource[M, ID, U]) allPages(ctx context.Context, request *query.Request) ([]M, error) {
	q := *request
	q.Unpaged, q.SkipTotal = false, false
	q.Page, q.Limit = 0, 0
	backward := q.Before != ""
	cursoring := q.After != "" || backward
	offsetWalk := q.Offset != 0 && !cursoring
	skip := 0
	if request.Unpaged && q.Offset > 0 && !cursoring {
		skip, q.Offset, offsetWalk = q.Offset, 0, false
	}
	if len(q.Sort) == 0 && !(q.Distinct && len(q.Select) > 0 && !selectsPrimaryKey(this.meta, q.Select)) {
		q.Sort = query.Sorts{{Field: this.meta.PK.Name}}
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
		page, err := this.list(ctx, &q)
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

			expectedTotal = page.Total
			if offsetWalk || (request.Unpaged && request.Offset > 0) {
				expectedTotal -= int64(request.Offset)
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
			q.Offset = request.Offset + received
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

	total := last.Total
	if expectedTotal >= 0 {
		total = expectedTotal
	} else if offsetWalk || (request.Unpaged && request.Offset > 0) {
		total -= int64(request.Offset)
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

func (this *Resource[M, ID, U]) GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	var zero M
	request, err := ToRequest(options...)
	if err != nil {
		return zero, err
	}

	port.NarrowForEntity(request)
	if entityNeedsList(request) {
		return this.getByIDThroughList(ctx, id, request)
	}
	raw, err := this.tr.Do(ctx, &Call{Method: MethodGet, ID: port.FormatID(id), Query: request})
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
}

func entityNeedsList(request *query.Request) bool {
	if request == nil {
		return false
	}
	if !request.Filter.IsZero() {
		return true
	}
	for _, pre := range request.Preload {
		if !pre.Filter.IsZero() || len(pre.Sort) > 0 || pre.MaxRows > 0 {
			return true
		}
	}
	return false
}

func (this *Resource[M, ID, U]) getByIDThroughList(ctx context.Context, id ID, request *query.Request) (M, error) {
	var zero M

	idFilter, err := crud.MarshalPredicate(crud.Eq(this.meta.PK.Name, port.FormatID(id)))
	if err != nil {
		return zero, &OptionError{
			Option: "GetByID filter",
			Reason: "the primary-key value cannot be encoded for the List fallback: " + err.Error(),
		}
	}
	if request.Filter.IsZero() {
		request.Filter = query.RawFilter(string(idFilter))
	} else {
		filter, err := request.Filter.MarshalJSON()
		if err != nil {
			return zero, err
		}
		request.Filter = query.RawFilter(`{"and":[` + string(filter) + `,` + string(idFilter) + `]}`)
	}
	page, err := this.list(ctx, request)
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

func (this *Resource[M, ID, U]) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	request, err := ToRequest(options...)
	if err != nil {
		return 0, err
	}
	port.NarrowForCount(request)
	raw, err := this.tr.Do(ctx, &Call{Method: MethodCount, Query: request})
	if err != nil {
		return 0, err
	}
	body, err := decode[struct {
		Count exactInt64 `json:"count"`
	}](raw, "a count")
	return int64(body.Count), err
}

func (this *Resource[M, ID, U]) Save(ctx context.Context, m *M) (M, error) {
	var zero M
	call, err := this.saveCall(m)
	if err != nil {
		return zero, err
	}
	raw, err := this.tr.Do(ctx, call)
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
}

func (this *Resource[M, ID, U]) SaveOnly(ctx context.Context, m *M) error {
	call, err := this.saveCall(m)
	if err != nil {
		return err
	}
	_, err = this.tr.Do(ctx, call)
	return err
}

func (this *Resource[M, ID, U]) saveCall(m *M) (*Call, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("remote: encoding the model: %w", err)
	}
	has, err := this.meta.HasID(m)
	if err != nil {
		return nil, err
	}

	call := &Call{Method: MethodCreate, Body: body}
	if has {
		key, err := keyOf[ID](this.meta, m)
		if err != nil {
			return nil, err
		}
		call = &Call{Method: MethodReplace, ID: key, Body: body}
	}
	return call, nil
}

func (this *Resource[M, ID, U]) Update(ctx context.Context, id ID, dataTransferObject U, options ...crud.Option) (M, error) {
	var zero M
	if err := refuseOptions("Update", options); err != nil {
		return zero, err
	}
	body, err := json.Marshal(dataTransferObject)
	if err != nil {
		return zero, fmt.Errorf("remote: encoding the patch: %w", err)
	}
	raw, err := this.tr.Do(ctx, &Call{Method: MethodUpdate, ID: port.FormatID(id), Body: body})
	if err != nil {
		return zero, err
	}
	return decode[M](raw, "an entity")
}

func (this *Resource[M, ID, U]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	switch len(ids) {
	case 0:
		return 0, nil
	case 1:
		raw, err := this.tr.Do(ctx, &Call{Method: MethodDelete, ID: port.FormatID(ids[0])})
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
	raw, err := this.tr.Do(ctx, &Call{Method: MethodBulkDelete, IDs: keys})
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

type exactInt64 int64

func (this *exactInt64) UnmarshalJSON(raw []byte) error {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		value, err := strconv.ParseInt(asString, 10, 64)
		if err != nil {
			return fmt.Errorf("exact integer %q: %w", asString, err)
		}
		*this = exactInt64(value)
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
	*this = exactInt64(value)
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

func refuseOptions(method string, options []crud.Option) error {
	for _, o := range options {
		if o != nil {
			return &OptionError{"an option on " + method,
				"the route carries no query document, so nothing would be narrowed"}
		}
	}
	return nil
}

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
