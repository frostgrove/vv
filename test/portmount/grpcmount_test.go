package portmount

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/rpc/crudgrpc"
)

// The fourth transport. Everything in this file is the same claim mount_test.go
// makes for the three HTTP bindings, extended to a protocol that is not HTTP —
// which is the only thing that has ever measured [[D-045]]'s sentence about the
// shared half being transport-neutral.
//
// The HTTP arms compare body bytes; this one compares documents and statuses,
// because a gRPC response is a proto message and its map fields have no wire
// order. What is compared unchanged is the thing with teeth: the command the
// binding handed the service.

const grpcResource = "Widget"

// grpcServe runs one handler on an in-process server and answers a client that
// calls it by full method name.
func grpcServe(t *testing.T, desc *grpc.ServiceDesc) *grpcClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	srv.RegisterService(desc, nil)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialling the in-process server: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	})
	return &grpcClient{t: t, conn: conn, service: desc.ServiceName}
}

type grpcClient struct {
	t       *testing.T
	conn    *grpc.ClientConn
	service string
}

func (c *grpcClient) call(method string, in *structpb.Struct) (*structpb.Struct, *status.Status) {
	c.t.Helper()
	out := &structpb.Struct{}
	err := c.conn.Invoke(context.Background(), "/"+c.service+"/"+method, in, out)
	if err == nil {
		return out, nil
	}
	st, ok := status.FromError(err)
	if !ok {
		c.t.Fatalf("%s answered an error with no status: %v", method, err)
	}
	return nil, st
}

func grpcDoc(t *testing.T, raw string) *structpb.Struct {
	t.Helper()
	st := &structpb.Struct{}
	if raw == "" {
		return st
	}
	if err := st.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("the fixture %s is not a JSON object: %v", raw, err)
	}
	return st
}

// ---------------------------------------------------------------------------

// grpcCall is one request in the vocabulary of the fourth transport, beside the
// HTTP spelling of the same thing.
type grpcCall struct {
	method  string
	request string
}

// TestTheSameServiceMountsOnAllFourTransports is this phase's control case.
//
// One port.Service value; four transports; the same *command* recorded by all
// four, the same entity document, and the same violation list. The command is
// the assertion with teeth: equal answers would pass for four bindings that
// reached the service by different routes, and the moment one of them
// re-derives a rule the service owns — narrows a count itself, coerces a key
// differently, clears a field before handing it over — the recorded command
// diverges and this fails naming the offender.
//
// Verified the way [[D-045]] records: putting NarrowForCount back into one
// transport makes the recorded command diverge and this test names which one.
func TestTheSameServiceMountsOnAllFourTransports(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		grpc                       grpcCall
	}{
		{
			name: "a list through the JSON document", method: http.MethodPost, target: "/widgets/query",
			body: `{"limit":3,"sort":["-price"]}`,
			grpc: grpcCall{"List", `{"limit":3,"sort":["-price"]}`},
		},
		{
			name: "a count with the paging a count must drop", method: http.MethodPost, target: "/widgets/count",
			body: `{"page":4,"limit":9,"sort":["-price"],"filter":{"price":{"gte":100}}}`,
			grpc: grpcCall{"Count", `{"page":4,"limit":9,"sort":["-price"],"filter":{"price":{"gte":100}}}`},
		},
		{
			name: "one entity, with the shaping a keyed read keeps", method: http.MethodGet, target: "/widgets/42?select=name",
			grpc: grpcCall{"Get", `{"id":"42","query":{"select":["name"]}}`},
		},
		{
			name: "a create carrying what it may not choose", method: http.MethodPost, target: "/widgets",
			body: `{"id":999,"name":"bolt","price":250,"createdAt":"2001-02-03T04:05:06Z"}`,
			grpc: grpcCall{"Create", `{"id":999,"name":"bolt","price":250,"createdAt":"2001-02-03T04:05:06Z"}`},
		},
		{
			name: "a patch", method: http.MethodPatch, target: "/widgets/42", body: `{"name":"patched"}`,
			grpc: grpcCall{"Update", `{"id":"42","patch":{"name":"patched"}}`},
		},
		{
			name: "a replace", method: http.MethodPut, target: "/widgets/42", body: `{"id":999,"name":"replaced"}`,
			grpc: grpcCall{"Replace", `{"id":"42","entity":{"id":999,"name":"replaced"}}`},
		},
		{
			name: "a delete", method: http.MethodDelete, target: "/widgets/42",
			grpc: grpcCall{"Delete", `{"id":"42"}`},
		},
		{
			name: "a bulk delete", method: http.MethodPost, target: "/widgets/bulk-delete", body: `{"ids":[1,2,3]}`,
			grpc: grpcCall{"BulkDelete", `{"ids":["1","2","3"]}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The three HTTP bindings first, so a divergence among them is
			// reported by the test that is about them.
			var (
				want []command
				body []byte
			)
			for i, b := range bindings {
				svc := newRecorder()
				_, raw := b.serve(t, svc, tc.method, tc.target, tc.body)
				if i == 0 {
					want, body = svc.got, raw
					continue
				}
				if !reflect.DeepEqual(svc.got, want) {
					t.Fatalf("%s handed the service %+v and crudnet handed it %+v", b.name, svc.got, want)
				}
			}

			// And the fourth.
			svc := newRecorder()
			c := grpcServe(t, crudgrpc.Serving(svc).Desc(grpcResource))
			out, st := c.call(tc.grpc.method, grpcDoc(t, tc.grpc.request))
			if st != nil {
				t.Fatalf("crudgrpc answered %s: %s", st.Code(), st.Message())
			}
			if got, first := commandsOf(t, svc.got), commandsOf(t, want); got != first {
				t.Fatalf("crudgrpc handed the service %s and the HTTP bindings handed it %s — one of them is re-deriving a rule the service owns",
					got, first)
			}

			// The documents agree, which is what makes the answer the same API
			// rather than the same plumbing. Compared as documents and not as
			// bytes: a proto map has no wire order.
			if got, want := out.AsMap(), documentOf(t, body); !reflect.DeepEqual(got, want) {
				t.Fatalf("crudgrpc answered %v and the HTTP bindings answered %v", got, want)
			}
		})
	}
}

// commandsOf renders the commands as the document they carry.
//
// The gRPC arm is compared this way and the three HTTP ones with DeepEqual,
// and the difference is not a weakening. query.ParseQuery leaves an empty
// Sorts{} where the JSON decoder leaves nil, so the query-string front door and
// the document front door already disagree about slice headers — between two
// *HTTP* bindings, before gRPC existed. What a command says is the thing this
// test is about, and every field of query.Request is omitempty, so two
// documents that say the same thing render the same and two that differ in any
// value still differ here.
func commandsOf(t *testing.T, cmds []command) string {
	t.Helper()
	raw, err := json.Marshal(cmds)
	if err != nil {
		t.Fatalf("a recorded command does not marshal: %v", err)
	}
	return string(raw)
}

// documentOf reads an HTTP body as the document a gRPC Struct would carry, so
// the two can be compared at all. Numbers on both sides are float64, which is
// what encoding/json and google.protobuf.Value both produce.
func documentOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the HTTP body is not a JSON object: %v in %s", err, raw)
	}
	return doc
}

// [[D-050]]'s control extended to the fourth transport: the generated hop is
// wired the same way by every binding, so the same violation names the same
// client key wherever it is mounted.
func TestAGeneratedResourceResolvesTheSameFieldOnAllFourTransports(t *testing.T) {
	fault := func() error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field("Name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}
	const body = `{"label":"bolt","price":250}`

	svc := newRecorder()
	svc.repo.err = fault()
	c := grpcServe(t, crudgrpc.ServingFor(svc, WidgetMapper{}).Desc(grpcResource))
	_, st := c.call("Create", grpcDoc(t, body))
	if st == nil || st.Code() != codes.AlreadyExists {
		t.Fatalf("crudgrpc answered %v for a duplicate key", st)
	}
	if got := grpcFieldOf(t, st); got != "label" {
		t.Fatalf("crudgrpc rendered the field as %q, want the key the client sent", got)
	}

	// The control. Mounted with Serving — Identity, no map — the same violation
	// has nothing to translate it, and this transport has no raw-body fallback
	// behind the declared hops, so the client is handed the model's own field
	// name back. Without this the assertion above passes for a binding that
	// never wired the hop at all.
	plain := newRecorder()
	plain.repo.err = fault()
	c2 := grpcServe(t, crudgrpc.Serving(plain).Desc(grpcResource))
	_, st2 := c2.call("Create", grpcDoc(t, `{"name":"bolt","price":250}`))
	if st2 == nil {
		t.Fatal("crudgrpc succeeded on a duplicate key")
	}
	if got := grpcFieldOf(t, st2); got != "Name" {
		t.Fatalf("without a map crudgrpc rendered %q; the generated map answering \"label\" proves nothing unless the two differ", got)
	}
}

// grpcFieldOf reads the dotted field path out of the first field violation.
func grpcFieldOf(t *testing.T, st *status.Status) string {
	t.Helper()
	for _, d := range st.Details() {
		br, ok := d.(*errdetails.BadRequest)
		if !ok {
			continue
		}
		if len(br.GetFieldViolations()) != 1 {
			t.Fatalf("the status carries %d field violations, want one", len(br.GetFieldViolations()))
		}
		return br.GetFieldViolations()[0].GetField()
	}
	t.Fatalf("the status carries no BadRequest detail: %v", st.Details())
	return ""
}

// One code, spelled the same on both transports. A client that speaks HTTP and
// gRPC branches on one table, which is what `ROADMAP-errors.md` §11 asks of a
// stable machine code.
func TestTheSameCodeIsSpelledTheSameOnBothTransports(t *testing.T) {
	fault := func() error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field("Name").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}
	const body = `{"label":"bolt","price":250}`

	http4 := newRecorder()
	http4.repo.err = fault()
	_, raw := bindings[0].mappedServe(t, http4, http.MethodPost, "/widgets", body)
	envelopeCode := codeOf(t, raw)

	grpcSvc := newRecorder()
	grpcSvc.repo.err = fault()
	c := grpcServe(t, crudgrpc.ServingFor(grpcSvc, WidgetMapper{}).Desc(grpcResource))
	_, st := c.call("Create", grpcDoc(t, body))
	if st == nil {
		t.Fatal("crudgrpc succeeded on a duplicate key")
	}
	reason := grpcReasonOf(t, st)

	if envelopeCode != reason {
		t.Fatalf("the envelope says error_code %q and the status detail says reason %q", envelopeCode, reason)
	}
	// The control: the literal spelling, so an implementation that
	// UPPER_SNAKE_CASEs one side fails here rather than passing a check that
	// both are non-empty.
	if envelopeCode != "unique" {
		t.Fatalf("both transports agree on %q, and the code this library declares is %q", envelopeCode, "unique")
	}
}

func codeOf(t *testing.T, raw []byte) string {
	t.Helper()
	var env struct {
		Errors struct {
			Validation []struct {
				Code string `json:"error_code"`
			} `json:"validation"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("the body is not the envelope: %v\n%s", err, raw)
	}
	if len(env.Errors.Validation) != 1 {
		t.Fatalf("the body carries %d validation violations, want one: %s", len(env.Errors.Validation), raw)
	}
	return env.Errors.Validation[0].Code
}

func grpcReasonOf(t *testing.T, st *status.Status) string {
	t.Helper()
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok && len(br.GetFieldViolations()) == 1 {
			return br.GetFieldViolations()[0].GetReason()
		}
	}
	t.Fatalf("the status carries no single field violation: %v", st.Details())
	return ""
}

// A malformed request is the same class on both transports, and neither says
// anything a client cannot act on.
func TestARefusalIsTheSameClassOnBothTransports(t *testing.T) {
	for _, tc := range []struct {
		name, target, request string
		method                string
		wantStatus            int
		wantCode              codes.Code
	}{
		{
			name: "a key that does not parse", method: "Get",
			target: "/widgets/nope", request: `{"id":"nope"}`,
			wantStatus: http.StatusBadRequest, wantCode: codes.InvalidArgument,
		},
		{
			name: "a filter on a field the model lacks", method: "List",
			target: "/widgets?f=nope:eq:1", request: `{"filter":{"nope":{"eq":1}}}`,
			wantStatus: http.StatusBadRequest, wantCode: codes.InvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newRecorder()
			gotStatus, raw := bindings[0].serve(t, svc, http.MethodGet, tc.target, "")
			if gotStatus != tc.wantStatus {
				t.Fatalf("the HTTP binding answered %d, want %d: %s", gotStatus, tc.wantStatus, raw)
			}

			grpcSvc := newRecorder()
			c := grpcServe(t, crudgrpc.Serving(grpcSvc).Desc(grpcResource))
			_, st := c.call(tc.method, grpcDoc(t, tc.request))
			if st == nil {
				t.Fatal("crudgrpc accepted a request the HTTP bindings refused")
			}
			if st.Code() != tc.wantCode {
				t.Fatalf("crudgrpc answered %s, want %s", st.Code(), tc.wantCode)
			}
			// Neither says anything internal, and both say something.
			if st.Message() == "" {
				t.Fatal("the status carries no message")
			}
			if strings.Contains(st.Message(), "widgets") {
				t.Fatalf("the status names the table: %q", st.Message())
			}
		})
	}
}
