package port

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

type widget struct {
	ID        int64      `db:"id,pk,auto" json:"id"`
	Name      string     `db:"name" json:"name"`
	Price     int        `db:"price" json:"price"`
	CreatedAt time.Time  `db:"created_at,generated" json:"createdAt"`
	Version   int        `db:"version,version" json:"version"`
	Digest    string     `db:"digest,serverowned" json:"digest"`
	DeletedAt *time.Time `db:"deleted_at,serverowned,tombstone" json:"deletedAt,omitempty"`
}

type widgetUpdate struct {
	Name *string `json:"name"`
}

var widgetMeta = func() *crud.Meta {
	m, err := crud.NewMeta[widget]("widgets")
	if err != nil {
		panic(err)
	}
	return m
}()

var forged = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

type call struct {
	method             string
	id                 int64
	ids                []int64
	model              widget
	dataTransferObject widgetUpdate
	options            *crud.Options
}

type fakeRepo struct {
	calls   []call
	deleted int64
	err     error
}

type restorableFakeRepo struct {
	*fakeRepo
	restored int64
}

func (this *restorableFakeRepo) Restore(_ context.Context, ids ...int64) (int64, error) {
	this.calls = append(this.calls, call{method: "Restore", ids: ids})
	return this.restored, this.err
}

func (this *fakeRepo) Meta() *crud.Meta { return widgetMeta }

func (this *fakeRepo) Get(_ context.Context, options ...crud.Option) (crud.PaginatedResponse[widget], error) {
	this.calls = append(this.calls, call{method: "Get", options: crud.Build(options...)})
	return crud.PaginatedResponse[widget]{}, this.err
}

func (this *fakeRepo) GetAll(_ context.Context, options ...crud.Option) ([]widget, error) {
	this.calls = append(this.calls, call{method: "GetAll", options: crud.Build(options...)})
	return nil, this.err
}

func (this *fakeRepo) GetByID(_ context.Context, id int64, options ...crud.Option) (widget, error) {
	this.calls = append(this.calls, call{method: "GetByID", id: id, options: crud.Build(options...)})
	return widget{ID: id}, this.err
}

func (this *fakeRepo) Count(_ context.Context, options ...crud.Option) (int64, error) {
	this.calls = append(this.calls, call{method: "Count", options: crud.Build(options...)})
	return 0, this.err
}

func (this *fakeRepo) Save(_ context.Context, m *widget) (widget, error) {
	this.calls = append(this.calls, call{method: "Save", model: *m})
	return *m, this.err
}

func (this *fakeRepo) Update(_ context.Context, id int64, dataTransferObject widgetUpdate, _ ...crud.Option) (widget, error) {
	this.calls = append(this.calls, call{method: "Update", id: id, dataTransferObject: dataTransferObject})
	return widget{ID: id}, this.err
}

func (this *fakeRepo) Delete(_ context.Context, ids ...int64) (int64, error) {
	this.calls = append(this.calls, call{method: "Delete", ids: ids})
	return this.deleted, this.err
}

func (this *fakeRepo) methods() []string {
	out := make([]string, len(this.calls))
	for i, c := range this.calls {
		out[i] = c.method
	}
	return out
}

func (this *fakeRepo) only(t *testing.T, method string) call {
	t.Helper()
	var found []call
	for _, c := range this.calls {
		if c.method == method {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the service called %s %d times, expected once; it called %v", method, len(found), this.methods())
	}
	return found[0]
}

type fakeService struct {
	*DefaultService[widget, int64, widgetUpdate]
	paths errs.Resolver
}

func (this *fakeService) Paths() errs.Resolver { return this.paths }

type mappingIn struct{}

func (mappingIn) Model(_ context.Context, in widget) (widget, error) { return in, nil }

func (mappingIn) Resolve(p errs.Path) (errs.Path, bool) {
	return append(errs.Path{errs.Named("payload")}, p...), true
}

type recordingHop struct {
	prefix errs.Step
	ran    bool
}

func (this *recordingHop) Resolve(p errs.Path) (errs.Path, bool) {
	this.ran = true
	return append(errs.Path{this.prefix}, p...), true
}

func TestTheDefaultServiceAppliesTheRulesInOrder(t *testing.T) {
	t.Run("create clears the server-owned fields before the hook", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)

		var seen widget
		got, err := service.Create(context.Background(), CreateCommand[widget]{
			Model: widget{ID: 999, Name: "bolt", CreatedAt: forged, Version: 99, Digest: "forged", DeletedAt: &forged},
			Before: func(m *widget) error {
				seen = *m
				m.Price = 7
				return nil
			},
		})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		if seen.ID != 0 {
			t.Fatalf("the hook was handed a client-chosen key of %d; it runs after the clearing, so it must see none", seen.ID)
		}
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("the hook was handed a forged %v in a generated column", seen.CreatedAt)
		}
		if seen.Version != 0 || seen.Digest != "" || seen.DeletedAt != nil {
			t.Fatalf("the hook was handed repository-owned state: version=%d digest=%q deletedAt=%v", seen.Version, seen.Digest, seen.DeletedAt)
		}
		stored := repository.only(t, "Save").model
		if stored.Price != 7 {
			t.Fatalf("the repository stored %+v; what the hook wrote is what is saved", stored)
		}
		if got.Price != 7 {
			t.Fatalf("the service answered %+v, want the row it saved", got)
		}
	})

	t.Run("and the control: with the key space handed over, the hook sees the client's key", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository, AllowClientID())

		var seen widget
		if _, err := service.Create(context.Background(), CreateCommand[widget]{
			Model:  widget{ID: 999, Name: "bolt", CreatedAt: forged, Version: 99, Digest: "forged", DeletedAt: &forged},
			Before: func(m *widget) error { seen = *m; return nil },
		}); err != nil {
			t.Fatalf("creating: %v", err)
		}
		if seen.ID != 999 {
			t.Fatalf("with AllowClientID the hook saw id %d, want the 999 the client sent — otherwise the subtest above proves nothing about ordering", seen.ID)
		}

		if !seen.CreatedAt.IsZero() {
			t.Fatalf("AllowClientID let a forged %v through in a generated column", seen.CreatedAt)
		}
		if seen.Version != 0 || seen.Digest != "" || seen.DeletedAt != nil {
			t.Fatalf("AllowClientID widened non-key ownership: version=%d digest=%q deletedAt=%v", seen.Version, seen.Digest, seen.DeletedAt)
		}
	})

	t.Run("a hook that refuses stops the write", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)
		boom := errors.New("no")

		if _, err := service.Create(context.Background(), CreateCommand[widget]{
			Before: func(*widget) error { return boom },
		}); !errors.Is(err, boom) {
			t.Fatalf("a refused create answered %v, want the hook's own error", err)
		}
		if len(repository.calls) != 0 {
			t.Fatalf("a refused create still reached the repository: %v", repository.methods())
		}
	})

	t.Run("replace looks the row up first, then clears, then sets the key, then hooks", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)

		var seen widget
		if _, err := service.Replace(context.Background(), ReplaceCommand[int64, widget]{
			ID:     42,
			Model:  widget{ID: 999, Name: "replaced", CreatedAt: forged, Version: 99, Digest: "forged", DeletedAt: &forged},
			Before: func(m *widget) error { seen = *m; return nil },
		}); err != nil {
			t.Fatalf("replacing: %v", err)
		}
		if got := repository.methods(); !slices.Equal(got, []string{"GetByID", "Save"}) {
			t.Fatalf("replace made %v, want the existence probe and then the write", got)
		}
		if seen.ID != 42 {
			t.Fatalf("the hook saw id %d, want the 42 the command named rather than the 999 in the body", seen.ID)
		}
		if !seen.CreatedAt.IsZero() {
			t.Fatalf("the hook was handed a forged %v in a generated column", seen.CreatedAt)
		}
		if seen.Version != 0 || seen.Digest != "" || seen.DeletedAt != nil {
			t.Fatalf("replace handed repository-owned state to the hook: version=%d digest=%q deletedAt=%v", seen.Version, seen.Digest, seen.DeletedAt)
		}
	})

	t.Run("a replace of a row that is not there never writes", func(t *testing.T) {
		repository := &fakeRepo{err: crud.ErrNotFound}
		service := NewService[widget, int64, widgetUpdate](repository)

		if _, err := service.Replace(context.Background(), ReplaceCommand[int64, widget]{ID: 999}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("replacing a missing row answered %v, want ErrNotFound", err)
		}
		if slices.Contains(repository.methods(), "Save") {
			t.Fatalf("a replace created a row at a key the client picked: %v", repository.methods())
		}
	})

	t.Run("the patch hook runs before the update", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)

		name := "from the hook"
		if _, err := service.Update(context.Background(), UpdateCommand[int64, widgetUpdate]{
			ID:     42,
			Before: func(u *widgetUpdate) error { u.Name = &name; return nil },
		}); err != nil {
			t.Fatalf("updating: %v", err)
		}
		c := repository.only(t, "Update")
		if c.id != 42 || c.dataTransferObject.Name == nil || *c.dataTransferObject.Name != name {
			t.Fatalf("the repository was asked to update %+v; the hook's mutation did not land", c)
		}
	})
}

func TestDeletingNothingIsAMissForOneRowAndZeroForASet(t *testing.T) {
	t.Run("one row", func(t *testing.T) {
		repository := &fakeRepo{deleted: 0}
		service := NewService[widget, int64, widgetUpdate](repository)
		if _, err := service.Delete(context.Background(), DeleteCommand[int64]{ID: 7}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("deleting a row that was not there answered %v, want ErrNotFound", err)
		}

		repository = &fakeRepo{deleted: 1}
		service = NewService[widget, int64, widgetUpdate](repository)
		if n, err := service.Delete(context.Background(), DeleteCommand[int64]{ID: 7}); err != nil || n != 1 {
			t.Fatalf("deleting a row that was there answered %d, %v", n, err)
		}
	})

	t.Run("a set", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)
		n, err := service.DeleteMany(context.Background(), BulkDeleteCommand[int64]{})
		if err != nil || n != 0 {
			t.Fatalf("deleting an empty set answered %d, %v, want 0 and no error", n, err)
		}
		// The empty set is passed down rather than answered here. The gate is a
		// decorator on the repository, and a service that short-circuits above
		// it authorizes nothing: bulk-deleting nothing answered 200 to a caller
		// with no principal at all.
		if len(repository.calls) != 1 || repository.calls[0].method != "Delete" {
			t.Fatalf("an empty set reached the repository as %v, want one Delete", repository.methods())
		}
	})
}

func TestRestoreIsASeparateApplicationUseCase(t *testing.T) {
	repository := &restorableFakeRepo{fakeRepo: &fakeRepo{}, restored: 1}
	service := NewService[widget, int64, widgetUpdate](repository)
	restore, ok := RestorableOf[int64](service)
	if !ok {
		t.Fatal("a repository with a real Restore capability did not publish the lifecycle use cases")
	}
	if n, err := restore.Restore(context.Background(), RestoreCommand[int64]{ID: 7}); err != nil || n != 1 {
		t.Fatalf("Restore = %d, %v", n, err)
	}
	if got := repository.only(t, "Restore").ids; !slices.Equal(got, []int64{7}) {
		t.Fatalf("Restore ids = %v", got)
	}

	repository = &restorableFakeRepo{fakeRepo: &fakeRepo{}, restored: 0}
	service = NewService[widget, int64, widgetUpdate](repository)
	restore, ok = RestorableOf[int64](service)
	if !ok {
		t.Fatal("replacement restorable repository lost its capability")
	}
	if _, err := restore.Restore(context.Background(), RestoreCommand[int64]{ID: 7}); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("missing Restore = %v, want ErrNotFound", err)
	}

	plain := NewService[widget, int64, widgetUpdate](&fakeRepo{})
	if _, structural := any(plain).(RestorableService[int64]); structural {
		t.Fatal("a hard-delete DefaultService structurally satisfies RestorableService")
	}
	if _, advertised := RestorableOf[int64](plain); advertised {
		t.Fatal("a hard-delete service advertised a restore route")
	}
}

func TestTheReadsNarrowTheDocumentAndAppendTheCallersOptions(t *testing.T) {
	tenant := crud.Where(crud.Eq("Price", 1))

	t.Run("count drops the paging", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)

		request := &query.Request{Page: 3, Limit: 10, Sort: query.Sorts{{Field: "Name"}}}
		if _, err := service.Count(context.Background(), CountCommand{Query: request, Options: []crud.Option{tenant}}); err != nil {
			t.Fatalf("counting: %v", err)
		}
		o := repository.only(t, "Count").options
		if o.Limit != 0 || o.Offset != 0 || len(o.Sort) != 0 {
			t.Fatalf("the count carried limit=%d offset=%d sort=%v; a page of a count is not a count", o.Limit, o.Offset, o.Sort)
		}
		if o.Predicate() == nil {
			t.Fatal("the caller's options never reached the repository")
		}
	})

	t.Run("a keyed read keeps its eligibility filter and drops paging", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)

		request := &query.Request{
			Page: 2, Limit: 5, After: "opaque", Unpaged: true, SkipTotal: true, Distinct: true,
			Terms: []query.Term{{Path: "Name", Op: "eq", Values: query.Strings{"bolt"}}},
		}
		if _, err := service.Get(context.Background(), GetCommand[int64]{ID: 42, Query: request}); err != nil {
			t.Fatalf("reading: %v", err)
		}
		c := repository.only(t, "GetByID")
		if c.id != 42 {
			t.Fatalf("the repository was asked for id %d, want 42", c.id)
		}
		if c.options.Predicate() == nil || c.options.Limit != 0 || c.options.After != "" || c.options.Unpaged || c.options.NoTotal || c.options.Distinct {
			t.Fatalf("a keyed read did not preserve only its eligibility filter: %+v", c.options)
		}
		sql, args, err := crud.NewSQL(crud.Postgres{}, widgetMeta).Predicate(c.options.Predicate()).Done()
		if err != nil {
			t.Fatalf("the keyed-read filter does not resolve: %v", err)
		}
		if sql != `"name" = $1` || !reflect.DeepEqual(args, []any{"bolt"}) {
			t.Fatalf("keyed-read filter = %s %#v, want name = bolt", sql, args)
		}
	})

	t.Run("a list keeps both, in that order", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)

		request := &query.Request{Terms: []query.Term{{Path: "Name", Op: "eq", Values: query.Strings{"bolt"}}}}
		if _, err := service.List(context.Background(), ListCommand{Query: request, Options: []crud.Option{tenant}}); err != nil {
			t.Fatalf("listing: %v", err)
		}
		o := repository.only(t, "Get").options
		sql, args, err := crud.NewSQL(crud.Postgres{}, widgetMeta).Predicate(o.Predicate()).Done()
		if err != nil {
			t.Fatalf("the compiled filter does not resolve: %v", err)
		}
		if want := `("name" = $1 AND "price" = $2)`; sql != want {
			t.Fatalf("the filter compiled to %s, want %s — the client's terms ANDed with the caller's options", sql, want)
		}
		if want := []any{"bolt", 1}; !reflect.DeepEqual(args, want) {
			t.Fatalf("bound %#v, want %#v", args, want)
		}
	})

	t.Run("no document at all is a plain read", func(t *testing.T) {
		repository := &fakeRepo{}
		service := NewService[widget, int64, widgetUpdate](repository)
		if _, err := service.List(context.Background(), ListCommand{}); err != nil {
			t.Fatalf("a command with no query document answered %v", err)
		}
		if o := repository.only(t, "Get").options; o.Predicate() != nil {
			t.Fatalf("a command with no query document compiled to a filter: %+v", o)
		}
	})
}

func TestReadNarrowingDoesNotMutateARequestReusedForAList(t *testing.T) {
	for _, tc := range []struct {
		name string
		read func(*DefaultService[widget, int64, widgetUpdate], *query.Request) error
	}{
		{
			name: "Count",
			read: func(service *DefaultService[widget, int64, widgetUpdate], request *query.Request) error {
				_, err := service.Count(context.Background(), CountCommand{Query: request})
				return err
			},
		},
		{
			name: "Get",
			read: func(service *DefaultService[widget, int64, widgetUpdate], request *query.Request) error {
				_, err := service.Get(context.Background(), GetCommand[int64]{ID: 1, Query: request})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepo{}
			service := NewService[widget, int64, widgetUpdate](repository, WithQuery(&query.Config{MaxLimit: 7}))
			request := &query.Request{Limit: 99, Page: 3, Terms: []query.Term{{Path: "Name", Values: query.Strings{"bolt"}}}}
			if err := tc.read(service, request); err != nil {
				t.Fatalf("read: %v", err)
			}
			if _, err := service.List(context.Background(), ListCommand{Query: request}); err != nil {
				t.Fatalf("list: %v", err)
			}
			if got := repository.only(t, "Get").options.Limit; got != 7 {
				t.Fatalf("list limit = %d, want the endpoint cap after reusing its request", got)
			}
		})
	}
}

func TestWithQueryBoundsTheServiceAndNotTheTransport(t *testing.T) {
	repository := &fakeRepo{}
	config := &query.Config{Filterable: []string{"Name"}}
	service := NewService[widget, int64, widgetUpdate](repository, WithQuery(config))

	request := &query.Request{Terms: []query.Term{{Path: "Price", Op: "eq", Values: query.Strings{"1"}}}}
	if _, err := service.List(context.Background(), ListCommand{Query: request}); err == nil {
		t.Fatal("a filter outside the allow-list was accepted")
	}

	request = &query.Request{Terms: []query.Term{{Path: "Name", Op: "eq", Values: query.Strings{"bolt"}}}}
	if _, err := service.List(context.Background(), ListCommand{Query: request}); err != nil {
		t.Fatalf("a filter on the allow-list was refused: %v", err)
	}
}

func TestWithQueryForSelectsADeclaredVocabularyPerRequest(t *testing.T) {
	type vocabularyKey struct{}
	repository := &fakeRepo{}
	service := NewService[widget, int64, widgetUpdate](repository,
		WithQueryFor(
			&query.Config{Filterable: []string{"Name"}},
			map[string]*query.Config{"admin": {Filterable: []string{"Name", "Price"}}},
			func(ctx context.Context) string {
				name, _ := ctx.Value(vocabularyKey{}).(string)
				return name
			},
		),
	)
	price := &query.Request{Terms: []query.Term{{Path: "Price", Values: query.Strings{"1"}}}}
	if _, err := service.List(context.Background(), ListCommand{Query: price}); err == nil {
		t.Fatal("the default vocabulary exposed the admin-only Price field")
	}
	adminCtx := context.WithValue(context.Background(), vocabularyKey{}, "admin")
	if _, err := service.List(adminCtx, ListCommand{Query: price}); err != nil {
		t.Fatalf("the declared admin vocabulary was refused: %v", err)
	}
	unknownCtx := context.WithValue(context.Background(), vocabularyKey{}, "unknown")
	if _, err := service.List(unknownCtx, ListCommand{Query: price}); err == nil {
		t.Fatal("an undeclared vocabulary silently fell back to the permissive default")
	} else if qerr, ok := err.(*query.Error); !ok || qerr.Path != "queryConfig" {
		t.Fatalf("err = %v, want a query refusal naming queryConfig", err)
	}
}

func TestWithQueryValidatesItsDeclarationWhenTheServiceIsBuilt(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a query allow-list entry that names nothing did not fail at service construction")
		}
	}()
	NewService[widget, int64, widgetUpdate](&fakeRepo{}, WithQuery(&query.Config{
		Filterable: []string{"DoesNotExist"},
	}))
}
