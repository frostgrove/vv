package crudgrpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/query"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/remote"
)

// remoteOver builds a client onto the in-process server a fake is already
// mounted on, so both ends of the round trip are the real thing.
func remoteOver(t *testing.T, c *client) *remote.Resource[Widget, int64, WidgetUpdate] {
	t.Helper()
	return remote.New[Widget, int64, WidgetUpdate](Transport(c.conn, resource))
}

// remoted mounts a resource this client can read whole.
//
// AllowUnpaged is not test scaffolding. There is no "every row" call on the
// wire — remote.GetAll is emulated with the unpaged flag — so a resource that
// never agreed to serve whole tables refuses it, on this transport exactly as on
// HTTP ([[D-060]], [[FL-013]]).
func remoted(t *testing.T, opts ...Option[Widget, int64, WidgetUpdate]) (*remote.Resource[Widget, int64, WidgetUpdate], *fakeRepo) {
	t.Helper()
	opts = append([]Option[Widget, int64, WidgetUpdate]{
		WithQuery[Widget, int64, WidgetUpdate](&query.Config{AllowUnpaged: true}),
	}, opts...)
	c, f := mount(t, opts...)
	return remoteOver(t, c), f
}

func clause(t *testing.T, o *crud.Options) string {
	t.Helper()
	sql, _, err := crud.NewSQL(crud.Postgres{}, widgetMeta).Predicate(o.Predicate()).Done()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return sql
}

// Every method, end to end, with no generated stub anywhere: the client calls
// by full method name and the server answers from a grpc.ServiceDesc it built
// out of closures. That is the Struct shape read from the other side.
func TestEveryMethodMakesTheRoundTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("Get", func(t *testing.T) {
		r, f := remoted(t)
		page, err := r.Get(ctx, crud.Limit(2), crud.Page(2))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Items) != 2 || page.Total != 5 {
			t.Fatalf("the page came back as %+v", page)
		}
		if got := f.only(t, "Get"); got.Opts.Limit != 2 || got.Opts.Page != 2 {
			t.Fatalf("the far side was asked for %+v", got.Opts)
		}
	})

	t.Run("GetAll", func(t *testing.T) {
		r, f := remoted(t)
		all, err := r.GetAll(ctx)
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("%d rows came back", len(all))
		}
		if !f.only(t, "Get").Opts.Unpaged {
			t.Fatal("GetAll asked for one page")
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		r, f := remoted(t)
		w, err := r.GetByID(ctx, 42, crud.Select("Name"))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if w.ID != 42 {
			t.Fatalf("row %d came back", w.ID)
		}
		if got := f.only(t, "GetByID"); got.ID != 42 {
			t.Fatalf("the far side was asked for row %d", got.ID)
		}
	})

	t.Run("Count", func(t *testing.T) {
		r, _ := remoted(t)
		n, err := r.Count(ctx)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 5 {
			t.Fatalf("the count came back as %d", n)
		}
	})

	t.Run("Save creates", func(t *testing.T) {
		r, f := remoted(t)
		w := Widget{Name: "washer", Price: 10}
		if err := r.Save(ctx, &w); err != nil {
			t.Fatalf("save: %v", err)
		}
		if w.ID != 7 || !w.CreatedAt.Equal(savedAt) {
			t.Fatalf("the model came back as %+v", w)
		}
		if got := f.only(t, "Save"); got.Model.Name != "washer" {
			t.Fatalf("the far side was handed %+v", got.Model)
		}
	})

	t.Run("Save replaces", func(t *testing.T) {
		r, f := remoted(t)
		w := Widget{ID: 42, Name: "bolt"}
		if err := r.Save(ctx, &w); err != nil {
			t.Fatalf("save: %v", err)
		}
		if w.ID != 42 {
			t.Fatalf("the key moved to %d", w.ID)
		}
		// Replace loads the row first, so a set key shows a GetByID before the
		// Save. A key that had gone out as a create would show none.
		if got := f.methods(); len(got) < 2 || got[0] != "GetByID" {
			t.Fatalf("a set key did not take the replace route: %v", got)
		}
	})

	t.Run("Update", func(t *testing.T) {
		r, f := remoted(t)
		name := "spanner"
		w, err := r.Update(ctx, 42, WidgetUpdate{Name: &name})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if w.Name != "spanner" {
			t.Fatalf("the row came back as %+v", w)
		}
		got := f.only(t, "Update")
		if got.DTO.Name == nil || *got.DTO.Name != "spanner" {
			t.Fatalf("the patch arrived as %+v", got.DTO)
		}
		// The three states survive a Struct, which is what the document goes
		// through encoding/json for: a key nobody sent is absent, not null.
		if got.DTO.Price != nil || got.DTO.Note.IsDefined() {
			t.Fatalf("a field nobody sent arrived defined: %+v", got.DTO)
		}
	})

	t.Run("Update writes an explicit null", func(t *testing.T) {
		r, f := remoted(t)
		if _, err := r.Update(ctx, 42, WidgetUpdate{Note: crud.Null[string]()}); err != nil {
			t.Fatalf("update: %v", err)
		}
		// The other half of the three states, and the control on the subtest
		// above: absent and null have to be told apart in both directions, or
		// one of the two assertions is measuring nothing.
		got := f.only(t, "Update").DTO.Note
		if !got.IsDefined() || !got.IsNull() {
			t.Fatalf("an explicit null arrived as %v", got)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		r, f := remoted(t)
		n, err := r.Delete(ctx, 42)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if n != 1 {
			t.Fatalf("%d rows went away", n)
		}
		if got := f.only(t, "Delete"); len(got.IDs) != 1 || got.IDs[0] != 42 {
			t.Fatalf("the far side was asked to delete %v", got.IDs)
		}
	})

	t.Run("Delete many", func(t *testing.T) {
		r, f := remoted(t)
		n, err := r.Delete(ctx, 1, 2, 3)
		if err != nil {
			t.Fatalf("bulk delete: %v", err)
		}
		if n != 3 {
			t.Fatalf("%d rows went away", n)
		}
		if got := f.only(t, "Delete"); len(got.IDs) != 3 {
			t.Fatalf("the far side was asked to delete %v", got.IDs)
		}
	})
}

// The same claim as the HTTP client's, on a transport that carries the whole
// document rather than a query string: a filter written in Go reaches the far
// side as the same narrowing a local repository would have been given.
func TestAFilterWrittenInGoArrivesAsTheSameNarrowing(t *testing.T) {
	r, f := remoted(t)

	opts := []crud.Option{
		crud.Where(crud.Eq("Name", "bolt")),
		crud.Where(crud.Gte("Price", 100)),
	}
	if _, err := r.Get(context.Background(), opts...); err != nil {
		t.Fatalf("list: %v", err)
	}

	want := clause(t, crud.Build(opts...))
	if want == "" {
		t.Fatal("the local options render nothing, so this proves nothing")
	}
	if got := clause(t, f.only(t, "Get").Opts); got != want {
		t.Fatalf("the far side was asked\n  %s\nwhere a local call asks\n  %s", got, want)
	}
}

// A refusal keeps its class, its violations and the sentinel a caller branches
// on — and carries nothing the driver said.
func TestAConflictArrivesAsAConflictWithItsViolations(t *testing.T) {
	r, f := remoted(t)
	f.err = errs.Conflict().
		Wrapping(crud.ErrConflict).
		Code(errs.CodeUnique).
		Field("name").Code(errs.CodeUnique).Message("that name is taken").
		Detail(errs.Detail{
			Dialect: "postgres", SQLState: "23505", Constraint: "widgets_name_key", Table: "widgets",
			Driver: errors.New(`pq: duplicate key value violates unique constraint "widgets_name_key"`),
		}).
		Fault()

	_, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("the conflict arrived as a success")
	}
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("the sentinel did not survive: %v", err)
	}
	fault, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("no fault came back, only %T", err)
	}
	if fault.Kind != errs.KindConflict || fault.Code != errs.CodeUnique {
		t.Fatalf("it arrived as %v/%v", fault.Kind, fault.Code)
	}
	if len(fault.Violations) != 1 {
		t.Fatalf("%d violations arrived", len(fault.Violations))
	}
	v := fault.Violations[0]
	if v.Code != errs.CodeUnique || v.Path.String() != "name" || v.Message != "that name is taken" {
		t.Fatalf("the violation arrived as %+v", v)
	}
	for _, secret := range []string{"widgets_name_key", "23505", "duplicate key", "pq:"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(v.Message, secret) {
			t.Fatalf("%q reached the caller", secret)
		}
	}
}

// An internal failure says nothing on this transport either: the status carries
// no details at all, so there is nothing for a client to reconstruct.
func TestAnInternalFailureArrivesEmpty(t *testing.T) {
	r, f := remoted(t)
	f.err = errors.New(`pq: password authentication failed for user "vv" on host db.internal:5432`)

	_, err := r.Get(context.Background())
	fault, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("no fault came back, only %T: %v", err, err)
	}
	if fault.Kind != errs.KindInternal {
		t.Fatalf("it arrived as %v", fault.Kind)
	}
	if len(fault.Violations) != 0 {
		t.Fatalf("an internal failure arrived carrying %d violations", len(fault.Violations))
	}
	for _, secret := range []string{"password", "db.internal", "5432", "pq:"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%q reached the caller", secret)
		}
	}
}

// This transport's own half of D-052's collapse, read backwards. A validation
// failure and a malformed request are both InvalidArgument on the wire, so the
// status word cannot tell them apart and the code has to — which is the job the
// code was given when the collapse was accepted.
func TestAValidationFailureAndAMalformedRequestAreToldApartByTheirCode(t *testing.T) {
	kindOf := func(t *testing.T, fail error) errs.Kind {
		t.Helper()
		r, f := remoted(t)
		f.err = fail
		_, err := r.Get(context.Background())
		fault, ok := errs.AsFault(err)
		if !ok {
			t.Fatalf("no fault came back, only %T: %v", err, err)
		}
		return fault.Kind
	}

	invalid := errs.Validation().Code(errs.CodeCheck).Field("price").Code(errs.CodeCheck).Fault()
	if got := kindOf(t, invalid); got != errs.KindValidation {
		t.Fatalf("a validation failure came back as %v, so 422 and 400 are the same answer here", got)
	}

	// The control, and it is the half that makes the assertion above mean
	// something: the same wire code with a different fault code has to come
	// back as the other kind. A client that answered KindValidation for every
	// InvalidArgument would pass the first check and fail this one.
	malformed := errs.BadRequest().Code(errs.CodeBadQuery).General().Code(errs.CodeBadQuery).Fault()
	if got := kindOf(t, malformed); got != errs.KindBadRequest {
		t.Fatalf("a malformed request came back as %v", got)
	}
}

// gRPC's version of the wrong-address problem. There is no 404 here, so the
// answer is Unimplemented — and a client must not read it as anything about a
// row. A read-only service is the case that reaches it without a typo.
func TestAnUnregisteredMethodIsNotAMissingRow(t *testing.T) {
	r, _ := remoted(t, ReadOnly[Widget, int64, WidgetUpdate]())

	w := Widget{Name: "washer"}
	err := r.Save(context.Background(), &w)
	if err == nil {
		t.Fatal("a create reached a read-only service")
	}
	if errors.Is(err, crud.ErrNotFound) {
		t.Fatal("a method that is not there arrived as a row that is not there")
	}
	var pe *remote.ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("it arrived as %T, which a caller cannot tell from a real answer: %v", err, err)
	}
	if pe.Status != "Unimplemented" {
		t.Fatalf("the status was reported as %q", pe.Status)
	}

	// The control: a read the service does register still classifies normally,
	// so the assertion above is not passing because everything is a protocol
	// error.
	r2, f := remoted(t)
	f.err = crud.ErrNotFound
	if _, err := r2.GetByID(context.Background(), 42); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("a real missing row arrived as %v", err)
	}
}
