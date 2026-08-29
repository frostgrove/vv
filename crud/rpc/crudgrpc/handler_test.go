package crudgrpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// ---------------------------------------------------------------------------
// the method set

// commands is one command exactly as the binding handed it over. The query
// document is copied rather than referenced: the service narrows it in place,
// so a binding that narrowed it first would be indistinguishable by the time
// the call returned.
type recordedCommand struct {
	Verb    string
	Query   query.Request
	ID      int64
	IDs     []int64
	Model   Widget
	Patched bool
	Hook    bool
}

// recorder is a Service that records what it was handed and then behaves like
// the default one.
type recorder struct {
	inner      *port.DefaultService[Widget, int64, WidgetUpdate]
	repository *fakeRepo
	got        []recordedCommand
}

func newRecorder() *recorder {
	repository := newFake()
	return &recorder{inner: port.NewService[Widget, int64, WidgetUpdate](repository), repository: repository}
}

func snap(request *query.Request) query.Request {
	if request == nil {
		return query.Request{}
	}
	return *request
}

func (this *recorder) Meta() *crud.Meta     { return this.inner.Meta() }
func (this *recorder) Paths() errs.Resolver { return this.inner.Paths() }
func (this *recorder) verbs() []string {
	out := make([]string, len(this.got))
	for i, c := range this.got {
		out[i] = c.Verb
	}
	return out
}

func (this *recorder) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[Widget], error) {
	this.got = append(this.got, recordedCommand{Verb: "List", Query: snap(cmd.Query)})
	return this.inner.List(ctx, cmd)
}

func (this *recorder) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	this.got = append(this.got, recordedCommand{Verb: "Count", Query: snap(cmd.Query)})
	return this.inner.Count(ctx, cmd)
}

func (this *recorder) Get(ctx context.Context, cmd port.GetCommand[int64]) (Widget, error) {
	this.got = append(this.got, recordedCommand{Verb: "Get", Query: snap(cmd.Query), ID: cmd.ID})
	return this.inner.Get(ctx, cmd)
}

func (this *recorder) Create(ctx context.Context, cmd port.CreateCommand[Widget]) (Widget, error) {
	this.got = append(this.got, recordedCommand{Verb: "Create", Model: cmd.Model, Hook: cmd.Before != nil})
	return this.inner.Create(ctx, cmd)
}

func (this *recorder) Update(ctx context.Context, cmd port.UpdateCommand[int64, WidgetUpdate]) (Widget, error) {
	this.got = append(this.got, recordedCommand{Verb: "Update", ID: cmd.ID, Patched: cmd.Patch.Name != nil, Hook: cmd.Before != nil})
	return this.inner.Update(ctx, cmd)
}

func (this *recorder) Replace(ctx context.Context, cmd port.ReplaceCommand[int64, Widget]) (Widget, error) {
	this.got = append(this.got, recordedCommand{Verb: "Replace", ID: cmd.ID, Model: cmd.Model, Hook: cmd.Before != nil})
	return this.inner.Replace(ctx, cmd)
}

func (this *recorder) Delete(ctx context.Context, cmd port.DeleteCommand[int64]) (int64, error) {
	this.got = append(this.got, recordedCommand{Verb: "Delete", ID: cmd.ID})
	return this.inner.Delete(ctx, cmd)
}

func (this *recorder) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[int64]) (int64, error) {
	this.got = append(this.got, recordedCommand{Verb: "DeleteMany", IDs: cmd.IDs})
	return this.inner.DeleteMany(ctx, cmd)
}

// Every command port declares has a method, and each one hands over the command
// it is named for. A method that quietly built the wrong command would answer
// something plausible and be wrong about what the API does.
func TestEveryCommandHasAMethod(t *testing.T) {
	for _, tc := range []struct {
		method, request, verb string
	}{
		{"List", `{"limit":3}`, "List"},
		{"Count", `{"page":4,"limit":9}`, "Count"},
		{"Get", `{"id":"42"}`, "Get"},
		{"Create", `{"name":"bolt"}`, "Create"},
		{"Update", `{"id":"42","patch":{"name":"patched"}}`, "Update"},
		{"Replace", `{"id":"42","entity":{"name":"replaced"}}`, "Replace"},
		{"Delete", `{"id":"42"}`, "Delete"},
		{"BulkDelete", `{"ids":["1","2","3"]}`, "DeleteMany"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			service := newRecorder()
			c := serve(t, Serving[Widget, int64, WidgetUpdate](service).Desc(resource))
			c.ok(tc.method, doc(t, tc.request))
			if got := service.verbs(); len(got) != 1 || got[0] != tc.verb {
				t.Fatalf("%s made the commands %v, want one %s", tc.method, got, tc.verb)
			}
		})
	}

	// The control: ReadOnly registers the three reads and nothing else, so a
	// write is Unimplemented rather than silently accepted. Without it the
	// eight above would pass for a binding that registers everything always.
	service := newRecorder()
	ro := serve(t, Serving[Widget, int64, WidgetUpdate](service, ReadOnly[Widget, int64, WidgetUpdate]()).Desc(resource))
	ro.ok("List", doc(t, `{}`))
	ro.ok("Count", doc(t, `{}`))
	ro.ok("Get", doc(t, `{"id":"42"}`))
	for _, method := range []string{"Create", "Update", "Replace", "Delete", "BulkDelete"} {
		if st := ro.fails(method, doc(t, `{"id":"42"}`)); st.Code() != codes.Unimplemented {
			t.Fatalf("under ReadOnly, %s answered %s, want Unimplemented", method, st.Code())
		}
	}
}

// The resource's own name is what the methods are registered under, so a
// per-method interceptor and an authorization rule can tell two resources
// apart.
func TestAResourceIsRegisteredUnderItsOwnName(t *testing.T) {
	if got := ServiceName("Widget"); got != "vv.crud.v1.Widget" {
		t.Fatalf("a bare name registered as %q", got)
	}
	// A name that already carries a package is used verbatim, so an
	// application can put its resources in a package of its own.
	if got := ServiceName("acme.catalog.v2.Widget"); got != "acme.catalog.v2.Widget" {
		t.Fatalf("a qualified name was rewritten to %q", got)
	}
}

// ---------------------------------------------------------------------------
// the document

// [[UC-003]] on a fourth transport: absent, explicitly null and a value are
// three outcomes, and a Struct is what has to keep them apart.
func TestAbsentNullAndValueSurviveTheStructRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch *structpb.Value
		want  func(*testing.T, crud.Opt[string])
	}{
		{"absent", nil, func(t *testing.T, o crud.Opt[string]) {
			if o.IsDefined() {
				t.Fatalf("a key the client omitted arrived as %v, want absent", o)
			}
		}},
		{"explicitly null", structpb.NewNullValue(), func(t *testing.T, o crud.Opt[string]) {
			if !o.IsDefined() || !o.IsNull() {
				t.Fatalf("an explicit null arrived as %v, want a defined null", o)
			}
		}},
		{"a value", structpb.NewStringValue("hand-fitted"), func(t *testing.T, o crud.Opt[string]) {
			if v, ok := o.Get(); !ok || v != "hand-fitted" {
				t.Fatalf("a value arrived as %v", o)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := mount(t)
			patch := &structpb.Struct{Fields: map[string]*structpb.Value{}}
			if tc.patch != nil {
				patch.Fields["note"] = tc.patch
			}
			request := &structpb.Struct{Fields: map[string]*structpb.Value{
				"id":    structpb.NewStringValue("42"),
				"patch": structpb.NewStructValue(patch),
			}}
			c.ok("Update", request)
			tc.want(t, f.only(t, "Update").DTO.Note)
		})
	}

	// The control on the pair that is easy to collapse: a Struct with no key
	// and a Struct with null must not produce the same Opt. Everything above
	// would pass for a decoder that read both as absent.
	states := map[string]bool{}
	for _, patch := range []*structpb.Struct{
		{Fields: map[string]*structpb.Value{}},
		{Fields: map[string]*structpb.Value{"note": structpb.NewNullValue()}},
	} {
		c, f := mount(t)
		c.ok("Update", &structpb.Struct{Fields: map[string]*structpb.Value{
			"id":    structpb.NewStringValue("42"),
			"patch": structpb.NewStructValue(patch),
		}})
		note := f.only(t, "Update").DTO.Note
		states[fmt.Sprintf("%v/%v", note.IsDefined(), note.IsNull())] = true
	}
	if len(states) != 2 {
		t.Fatalf("an absent key and an explicit null produced the same state %v; the three states collapsed to two", states)
	}
}

// The key is a string because google.protobuf.Value has no integer. This is the
// documented limit measured rather than claimed.
func TestAnInt64KeyIsCarriedAsAString(t *testing.T) {
	const big = int64(1)<<53 + 1 // the first int64 a double cannot spell

	c, f := mount(t)
	response := c.ok("Get", &structpb.Struct{Fields: map[string]*structpb.Value{
		"id": structpb.NewStringValue(fmt.Sprint(big)),
	}})
	if got := f.only(t, "GetByID").ID; got != big {
		t.Fatalf("the key arrived as %d, want %d — the string spelling did not survive", got, big)
	}

	// The control, and the reason the limit is written down: the same number
	// inside the entity document *is* a double and does lose precision. If this
	// ever stops being true the limit has been fixed and doc.go should say so.
	inEntity := int64(response.GetFields()["id"].GetNumberValue())
	if inEntity == big {
		t.Fatalf("the entity document carried %d exactly; doc.go still says a Struct number cannot", big)
	}

	// And the same key sent as a number is rejected: Struct has already rounded
	// it, so forwarding the nearby key would address a row the caller never
	// named. The string spelling is the only exact representation.
	c2, f2 := mount(t)
	st := c2.fails("Get", &structpb.Struct{Fields: map[string]*structpb.Value{
		"id": structpb.NewNumberValue(float64(big)),
	}})
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("rounded numeric ID answered %s, want InvalidArgument", st.Code())
	}
	if len(f2.calls) != 0 {
		t.Fatalf("rounded numeric ID reached the service: %#v", f2.calls)
	}
}

func TestMutationBodiesMustBeObjectsRatherThanNull(t *testing.T) {
	for _, tc := range []struct {
		method string
		body   string
	}{
		{"Update", `{"id":"42","patch":null}`},
		{"Replace", `{"id":"42","entity":null}`},
		{"Update", `{"id":"42"}`},
		{"Replace", `{"id":"42"}`},
		{"Update", `{"id":"42","patch":false}`},
		{"Replace", `{"id":"42","entity":[]}`},
	} {
		t.Run(tc.method+tc.body, func(t *testing.T) {
			c, f := mount(t)
			if st := c.fails(tc.method, doc(t, tc.body)); st.Code() != codes.InvalidArgument {
				t.Fatalf("status = %s, want InvalidArgument", st.Code())
			}
			if len(f.calls) != 0 {
				t.Fatalf("%s body reached the service: %#v", tc.body, f.calls)
			}
		})
	}
}

func TestListTotalUsesTheExactCountConvention(t *testing.T) {
	c, f := mount(t)
	f.page.Total = 9007199254740993
	f.page.TotalPages = 9007199254740993
	out := c.ok("List", doc(t, `{}`))
	if got := out.GetFields()["total"].GetStringValue(); got != "9007199254740993" {
		t.Fatalf("total = %q, want exact decimal string", got)
	}
	if got := out.GetFields()["totalPages"].GetStringValue(); got != "9007199254740993" {
		t.Fatalf("totalPages = %q, want exact decimal string", got)
	}
}

// A malformed request is a client mistake with a code on it, not a 500.
func TestAKeyThatDoesNotParseIsAClientMistake(t *testing.T) {
	c, f := mount(t)
	st := c.fails("Get", doc(t, `{"id":"nope"}`))
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("a key that does not parse answered %s", st.Code())
	}
	if len(f.calls) != 0 {
		t.Fatalf("the repository was reached with %v before the key was rejected", f.methods())
	}

	// The control: a key that does parse reaches the repository, so the
	// assertion above is about the refusal rather than a method that never
	// works.
	c2, f2 := mount(t)
	c2.ok("Get", doc(t, `{"id":"42"}`))
	if got := f2.only(t, "GetByID").ID; got != 42 {
		t.Fatalf("a valid key reached the repository as %d", got)
	}
}

// ---------------------------------------------------------------------------
// the rules the service owns

// Removing one row that is not there is a miss; removing an empty set is zero.
// The names differ from the HTTP triplet's because the answers are gRPC codes.
func TestDeletingNothingIsAMissForOneRowAndZeroForASet(t *testing.T) {
	service := newRecorder()
	service.repository.err = crud.ErrNotFound
	c := serve(t, Serving[Widget, int64, WidgetUpdate](service).Desc(resource))
	if st := c.fails("Delete", doc(t, `{"id":"42"}`)); st.Code() != codes.NotFound {
		t.Fatalf("deleting a row that is not there answered %s", st.Code())
	}

	for _, body := range []string{`{"ids":[]}`, `{}`, `{"ids":null}`} {
		t.Run(body, func(t *testing.T) {
			empty := newRecorder()
			c2 := serve(t, Serving[Widget, int64, WidgetUpdate](empty).Desc(resource))
			out := c2.ok("BulkDelete", doc(t, body))
			if got := out.GetFields()["deleted"].GetNumberValue(); got != 0 {
				t.Fatalf("deleting an empty set answered %v deleted", got)
			}
			if calls := empty.repository.calls; len(calls) != 0 {
				t.Fatalf("an empty set reached the repository as %v", calls)
			}
		})
	}
}

// Replace is not the way around AllowClientID: on a database-generated key it
// replaces and never creates ([[D-012]]).
func TestReplaceIsNotAWayAroundAllowClientID(t *testing.T) {
	service := newRecorder()
	c := serve(t, Serving[Widget, int64, WidgetUpdate](service).Desc(resource))
	c.ok("Replace", doc(t, `{"id":"42","entity":{"id":999,"name":"replaced"}}`))

	// The key came from the request and not from the document.
	if got := service.repository.only(t, "Save").Model.ID; got != 42 {
		t.Fatalf("the row was written at %d, want the key the request named", got)
	}
	// And the row had to be found first.
	if methods := service.repository.methods(); len(methods) != 2 || methods[0] != "GetByID" {
		t.Fatalf("the repository saw %v, want the existence check before the write", methods)
	}

	// The control: with AllowClientID the check is gone, so the assertion above
	// is about the guard rather than about a handler that always reads first.
	open := newRecorder()
	c2 := serve(t, ServingFor[Widget](
		port.NewService[Widget, int64, WidgetUpdate](open.repository, port.AllowClientID()),
		port.Identity[Widget]()).Desc(resource))
	c2.ok("Replace", doc(t, `{"id":"42","entity":{"name":"replaced"}}`))
	if methods := open.repository.methods(); len(methods) != 1 || methods[0] != "Save" {
		t.Fatalf("with AllowClientID the repository saw %v, want the write alone", methods)
	}
}

// A create cannot dictate the key or a generated column, and the clearing is
// the service's rather than this binding's.
func TestACreateIsClearedBelowTheBinding(t *testing.T) {
	service := newRecorder()
	c := serve(t, Serving[Widget, int64, WidgetUpdate](service).Desc(resource))
	c.ok("Create", doc(t, `{"id":999,"name":"bolt","createdAt":"2001-02-03T04:05:06Z"}`))

	if got := service.got[0].Model; got.ID != 999 || got.CreatedAt.IsZero() {
		t.Fatalf("the binding handed over %+v; clearing is the service's, and a binding that cleared first would hand over a zeroed model", got)
	}
	if got := service.repository.only(t, "Save").Model; got.ID != 0 || !got.CreatedAt.IsZero() {
		t.Fatalf("the repository was asked to write %+v, want the key and the generated column cleared", got)
	}
}

// ---------------------------------------------------------------------------
// the mapper

// WidgetInput, WidgetMapper and widgetPaths are what `vv -adapter` writes: a
// wire shape of the resource's own, a mapper onto the model, and the inverse of
// that mapping.
type WidgetInput struct {
	ID    int64  `json:"id"`
	Name  string `json:"label"`
	Price int    `json:"price"`
}

type WidgetMapper struct{}

func (WidgetMapper) Model(_ context.Context, in WidgetInput) (Widget, error) {
	return Widget{ID: in.ID, Name: in.Name, Price: in.Price}, nil
}

func (WidgetMapper) Resolve(p errs.Path) (errs.Path, bool) { return widgetPaths.Resolve(p) }

// Note and Secret are declared out: this input type does not carry them, and a
// map that claimed a key the client never sends would translate a violation to
// a path nothing in the request can be found at ([[D-043]]).
var widgetPaths = port.MustPathMap[Widget](port.PathMap{
	"ID":    port.At("id"),
	"Name":  port.At("label"),
	"Price": port.At("price"),
}, "Note", "Secret")

// NewFor infers all four type parameters from the repository and the mapper, so
// a call site still carries no generics.
func TestNewForInfersItsInputFromTheMapper(t *testing.T) {
	f := newFake()
	h := NewFor(Repository[Widget, int64, WidgetUpdate](f), Mapper[WidgetInput, Widget](WidgetMapper{}))
	var _ *HandlerFor[Widget, int64, WidgetUpdate, WidgetInput] = h
	if got := len(h.Desc(resource).Methods); got != 8 {
		t.Fatalf("the inferred handler registered %d methods", got)
	}
}

// A distinct input type reaches the model through the mapper, so a request
// document that is not the model's own JSON shape still writes the right row.
func TestADistinctInputDTOReachesTheModelThroughTheMapper(t *testing.T) {
	f := newFake()
	c := serve(t, NewFor(Repository[Widget, int64, WidgetUpdate](f), Mapper[WidgetInput, Widget](WidgetMapper{})).Desc(resource))
	c.ok("Create", doc(t, `{"label":"bolt","price":250}`))

	if got := f.only(t, "Save").Model; got.Name != "bolt" || got.Price != 250 {
		t.Fatalf("the mapper produced %+v", got)
	}

	// The control: the model's own key is not what the client sent, so a
	// handler that decoded straight into the model would have written an empty
	// name and passed nothing.
	f2 := newFake()
	c2 := serve(t, New[Widget, int64, WidgetUpdate](f2).Desc(resource))
	c2.ok("Create", doc(t, `{"label":"bolt","price":250}`))
	if got := f2.only(t, "Save").Model.Name; got != "" {
		t.Fatalf("without the mapper the name arrived as %q, so the mapping above proves nothing", got)
	}
}

// The mapper's hop reaches the rendered field, so a violation names the key the
// client sent rather than the model's field name ([[D-043]], [[D-050]]).
func TestAServicePathHopReachesTheRenderedField(t *testing.T) {
	taken := func() error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field("Name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}

	f := newFake()
	f.err = taken()
	c := serve(t, NewFor(Repository[Widget, int64, WidgetUpdate](f), Mapper[WidgetInput, Widget](WidgetMapper{})).Desc(resource))
	st := c.fails("Create", doc(t, `{"label":"bolt","price":250}`))
	if st.Code() != codes.AlreadyExists {
		t.Fatalf("a duplicate key answered %s", st.Code())
	}
	if got := fieldViolations(t, st)[0].GetField(); got != "label" {
		t.Fatalf("the field is %q, want the key the client sent", got)
	}

	// The control: mounted with New — Identity, no map — the same violation has
	// nothing to translate it and the client is handed the model's own field
	// name back. Without this the assertion above passes for a binding that
	// never wired the hop.
	f2 := newFake()
	f2.err = taken()
	c2 := serve(t, New[Widget, int64, WidgetUpdate](f2).Desc(resource))
	st2 := c2.fails("Create", doc(t, `{"label":"bolt","price":250}`))
	if got := fieldViolations(t, st2)[0].GetField(); got != "Name" {
		t.Fatalf("without a map the field is %q; the generated map answering \"label\" proves nothing unless the two differ", got)
	}
}

// ---------------------------------------------------------------------------
// the options

// A service-shaped option on Serving is refused where it is written rather than
// ignored at request time ([[D-021]]).
func TestAServiceShapedOptionOnServingIsRefusedAtDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  Option[Widget, int64, WidgetUpdate]
		want string
	}{
		{"WithQuery", WithQuery[Widget, int64, WidgetUpdate](&query.Config{}), "WithQuery"},
		{"AllowClientID", AllowClientID[Widget, int64, WidgetUpdate](), "AllowClientID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil {
					t.Fatalf("%s on Serving was accepted; a silently ignored bound is the failure this refusal exists for", tc.name)
				}
				if message, _ := p.(string); !strings.Contains(message, tc.want) || !strings.Contains(message, "crudgrpc.Serving") {
					t.Fatalf("the panic is %v; it has to name the option and the constructor", p)
				}
			}()
			Serving[Widget, int64, WidgetUpdate](newRecorder(), tc.opt)
		})
	}

	// The control: the same option on New is accepted, so the refusal is about
	// the finished service rather than about the option itself.
	New[Widget, int64, WidgetUpdate](newFake(), AllowClientID[Widget, int64, WidgetUpdate]())
}

// A presenter hides columns on every read shape.
func TestWithTransformHidesColumnsOnEveryReadShape(t *testing.T) {
	present := func(_ context.Context, w Widget) any {
		return map[string]any{"id": w.ID, "name": w.Name}
	}
	c, _ := mount(t, WithTransform[Widget, int64, WidgetUpdate](present))

	one := c.ok("Get", doc(t, `{"id":"42"}`))
	if _, leaked := one.GetFields()["secret"]; leaked {
		t.Fatalf("the presented entity carries the hidden column: %v", one.AsMap())
	}
	page := c.ok("List", doc(t, `{}`))
	if strings.Contains(fmt.Sprint(page.AsMap()), "swordfish") {
		t.Fatalf("the presented page carries the hidden column: %v", page.AsMap())
	}

	// The control: without the presenter the column is there, so the two
	// assertions above are about the presenter rather than a model that never
	// carried it.
	plain, _ := mount(t)
	if _, present := plain.ok("Get", doc(t, `{"id":"42"}`)).GetFields()["secret"]; !present {
		t.Fatal("the model does not carry the hidden column at all, so hiding it proves nothing")
	}
}

// MaxBulk caps one request, and the refusal is a client mistake.
func TestMaxBulkCapsOneRequest(t *testing.T) {
	c, f := mount(t, MaxBulk[Widget, int64, WidgetUpdate](2))
	if st := c.fails("BulkDelete", doc(t, `{"ids":["1","2","3"]}`)); st.Code() != codes.InvalidArgument {
		t.Fatalf("a bulk delete over the cap answered %s", st.Code())
	}
	if len(f.calls) != 0 {
		t.Fatalf("the repository was reached with %v despite the cap", f.methods())
	}
	// The control: at the cap it goes through.
	c.ok("BulkDelete", doc(t, `{"ids":["1","2"]}`))
}

// ---------------------------------------------------------------------------
// the interceptor

// Installing the interceptor twice renders once, and an error the application
// had already turned into a status is passed through untouched.
func TestInstallingTheInterceptorTwiceRendersOnce(t *testing.T) {
	f := newFake()
	f.err = errs.Conflict().Code(errs.CodeUnique).
		Field("name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	c := serve(t, New[Widget, int64, WidgetUpdate](f).Desc(resource),
		grpc.ChainUnaryInterceptor(Errors(), Errors()))

	st := c.fails("Create", doc(t, `{"name":"bolt"}`))
	if st.Code() != codes.AlreadyExists {
		t.Fatalf("with two interceptors a duplicate key answered %s", st.Code())
	}
	if got := len(fieldViolations(t, st)); got != 1 {
		t.Fatalf("the status carries %d field violations, want the one it was rendered from once", got)
	}

	// The control: an error the application had already turned into a status is
	// passed through unchanged, so the guard is not "we always overwrite".
	own := errors.New("a plain error the interceptor has to render")
	handler := func(_ context.Context, _ any) (any, error) { return nil, own }
	rendered, err := Errors()(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
	if rendered != nil {
		t.Fatalf("a failed call answered %v", rendered)
	}
	if Code(err) == codes.Internal && err.Error() == own.Error() {
		t.Fatal("the interceptor handed the raw error back, so it renders nothing")
	}
}

// The interceptor is the same table for a method this package did not write.
func TestTheInterceptorRendersAMethodOfYourOwn(t *testing.T) {
	handler := func(_ context.Context, _ any) (any, error) {
		return nil, errs.Validation().Code(errs.CodeCheck).
			Field("age").Code("too_young").Fault()
	}
	_, err := Errors()(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
	st := statusOf(t, err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("a validation failure from a hand-written method answered %s", st.Code())
	}
	if got := fieldViolations(t, st)[0].GetReason(); got != "too_young" {
		t.Fatalf("the reason is %q, want the code the method declared", got)
	}
}

// ---------------------------------------------------------------------------
// the locale

// The language a caller asked for reaches the message ladder, out of gRPC
// metadata rather than an HTTP header.
func TestTheRequestLocaleReachesTheMessageLadder(t *testing.T) {
	cat := errs.NewMessages(nil)
	for _, e := range []struct{ locale, text string }{
		{"fr", "cette adresse est deja prise"},
		{"", "that address is taken"},
	} {
		if err := cat.Add(e.locale, "name.unique", e.text); err != nil {
			t.Fatalf("declaring the %q message: %v", e.locale, err)
		}
	}

	message := func(t *testing.T, md metadata.MD) string {
		t.Helper()
		f := newFake()
		f.err = errs.Conflict().Code(errs.CodeUnique).
			Field("name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
		h := New[Widget, int64, WidgetUpdate](f,
			WithRenderer[Widget, int64, WidgetUpdate](NewRenderer(WithMessages(cat))))
		c := serve(t, h.Desc(resource)).with(md)
		st := c.fails("Create", doc(t, `{"name":"bolt"}`))
		if st.Code() != codes.AlreadyExists {
			t.Fatalf("a duplicate key answered %s: %s", st.Code(), st.Message())
		}
		return fieldViolations(t, st)[0].GetDescription()
	}

	for _, key := range LocaleKeys {
		t.Run(key, func(t *testing.T) {
			if got := message(t, metadata.Pairs(key, "fr-CA,fr;q=0.9")); got != "cette adresse est deja prise" {
				t.Fatalf("with %s the message is %q; the first tag is what the ladder is asked for", key, got)
			}
		})
	}

	// The control: with no metadata the default-locale entry wins. Without it a
	// catalogue that answered the same sentence whatever it was asked would
	// pass every leg above.
	if got := message(t, nil); got != "that address is taken" {
		t.Fatalf("with no locale metadata the message is %q, want the default-locale entry", got)
	}
}

// The locale the details report is the one the caller asked for.
func TestALocalizedMessageNamesTheRequestedLocale(t *testing.T) {
	f := newFake()
	f.err = errs.Conflict().Code(errs.CodeUnique).
		Field("name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	c := serve(t, New[Widget, int64, WidgetUpdate](f).Desc(resource)).
		with(metadata.Pairs("grpc-accept-language", "fr-CA,fr;q=0.9"))

	fv := fieldViolations(t, c.fails("Create", doc(t, `{"name":"bolt"}`)))[0]
	if got := fv.GetLocalizedMessage().GetLocale(); got != "fr-CA" {
		t.Fatalf("the localized message reports the locale %q, want the one the caller asked for", got)
	}

	// The control: a caller that asked for nothing gets no localized message,
	// rather than one claiming a translation it never requested.
	plain, plainRepo := mount(t)
	plainRepo.err = errs.Conflict().Code(errs.CodeUnique).
		Field("name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	if lm := fieldViolations(t, plain.fails("Create", doc(t, `{"name":"bolt"}`)))[0].GetLocalizedMessage(); lm != nil {
		t.Fatalf("a call with no locale carries the localized message %+v", lm)
	}
}
