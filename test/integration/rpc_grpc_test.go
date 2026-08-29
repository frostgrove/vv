//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/rpc/crudgrpc"
	"github.com/frostgrove/vv/errs"
)

// The fourth transport against a live database. http_port_test.go makes this
// claim for the three HTTP bindings; this is the arm that says the port layer
// carries a protocol that is not HTTP, on every engine.
//
// No t.Parallel anywhere: every test in this package shares the same physical
// tables.

// The gRPC binding holds the same Service type the three HTTP ones do, because
// a generic alias is the same type. The compile-time half of the claim; the
// tests below are the half that runs.
var _ crudgrpc.Service[Article, int64, ArticleUpdate] = articlePort{}

// grpcServe mounts one service on an in-process server and answers a client
// that calls it by full method name.
func grpcServe(t *testing.T, service articlePort) *grpcClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	srv := grpc.NewServer()
	crudgrpc.Serving[Article, int64, ArticleUpdate](service).Register(srv, "Article")
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialling %s: %v", lis.Addr(), err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return &grpcClient{t: t, conn: conn, service: crudgrpc.ServiceName("Article")}
}

// grpcServeRepo mounts a repository directly, for a fixture that has no service
// of its own.
func grpcServeRepo[M any, ID comparable, U any](t *testing.T, repository crudgrpc.Repository[M, ID, U]) *grpcClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	srv := grpc.NewServer()
	crudgrpc.New(repository).Register(srv, "Fixture")
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialling %s: %v", lis.Addr(), err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return &grpcClient{t: t, conn: conn, service: crudgrpc.ServiceName("Fixture")}
}

type grpcClient struct {
	t       *testing.T
	conn    *grpc.ClientConn
	service string
}

func (this *grpcClient) call(method, request string) (*structpb.Struct, *status.Status) {
	this.t.Helper()
	in := &structpb.Struct{}
	if request != "" {
		if err := in.UnmarshalJSON([]byte(request)); err != nil {
			this.t.Fatalf("the fixture %s is not a JSON object: %v", request, err)
		}
	}
	out := &structpb.Struct{}
	if err := this.conn.Invoke(context.Background(), "/"+this.service+"/"+method, in, out); err != nil {
		st, ok := status.FromError(err)
		if !ok {
			this.t.Fatalf("%s answered an error with no status: %v", method, err)
		}
		return nil, st
	}
	return out, nil
}

func (this *grpcClient) ok(method, request string) *structpb.Struct {
	this.t.Helper()
	out, st := this.call(method, request)
	if st != nil {
		this.t.Fatalf("%s answered %s: %s", method, st.Code(), st.Message())
	}
	return out
}

// One port.Service value, mounted on gRPC, against every engine.
//
// It is the same service http_port_test.go mounts on the three HTTP bindings —
// the same value, the same rules, the same generated query config — so what
// this measures is the transport and nothing else.
func TestOnePortServiceAlsoMountsOnGRPC(t *testing.T) {
	engines := 0
	for _, b := range blogs(t) {
		engines++
		t.Run(b.name, func(t *testing.T) {
			ann, _, generics, _, _ := seedBlog(t, b)
			service := newPortService(b)
			c := grpcServe(t, service)

			t.Run("a keyed read with a preload", func(t *testing.T) {
				out := c.ok("Get", `{"id":"`+itoa64(generics.ID)+`","query":{"preload":["Author"]}}`)
				doc := out.AsMap()
				if doc["Title"] != generics.Title {
					t.Fatalf("the row came back as %v", doc)
				}
				author, _ := doc["Author"].(map[string]any)
				if author == nil || author["Name"] == nil {
					t.Fatalf("the preload did not arrive: %v", doc)
				}
			})

			t.Run("a list through the query document", func(t *testing.T) {
				out := c.ok("List", `{"limit":2,"sort":["-ID"]}`)
				items, _ := out.AsMap()["items"].([]any)
				if len(items) != 2 {
					t.Fatalf("a page of two came back as %v", out.AsMap())
				}
			})

			t.Run("a count drops the paging", func(t *testing.T) {
				out := c.ok("Count", `{"page":2,"limit":1}`)
				if n, _ := out.AsMap()["count"].(float64); n < 2 {
					t.Fatalf("a count with paging in the document answered %v; a page of a count is not a count", out.AsMap())
				}
			})

			t.Run("the service's own rule holds here too", func(t *testing.T) {
				_, st := c.call("Create", `{"AuthorID":`+itoa64(ann.ID)+`,"Title":"forbidden title"}`)
				if st == nil || st.Code() != codes.PermissionDenied {
					t.Fatalf("the title the service refuses answered %v", st)
				}
			})

			// The control: a title it allows is written, and the key the client
			// sent is cleared below the binding. Without it the leg above
			// passes for a service that refuses everything, or for a binding
			// whose Create method was never registered.
			t.Run("and the control: a title it allows is written", func(t *testing.T) {
				out := c.ok("Create", `{"AuthorID":`+itoa64(ann.ID)+`,"Title":"through the port over gRPC","ID":999999}`)
				var got Article
				raw, err := out.MarshalJSON()
				if err != nil {
					t.Fatalf("the answer does not marshal: %v", err)
				}
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("the answer is not the row: %v in %s", err, raw)
				}
				if got.Title != "through the port over gRPC" {
					t.Fatalf("the row stored %q", got.Title)
				}
				if got.ID == 999999 {
					t.Fatal("the client chose its own key")
				}

				// And the row is really there, read back through the same
				// transport.
				c.ok("Get", `{"id":"`+itoa64(got.ID)+`"}`)

				n := c.ok("Delete", `{"id":"`+itoa64(got.ID)+`"}`)
				if deleted, _ := n.AsMap()["deleted"].(float64); deleted != 1 {
					t.Fatalf("deleting the row answered %v", n.AsMap())
				}
			})

			t.Run("a row that is not there is NotFound", func(t *testing.T) {
				_, st := c.call("Get", `{"id":"99999999"}`)
				if st == nil || st.Code() != codes.NotFound {
					t.Fatalf("a missing row answered %v", st)
				}
			})

			t.Run("a filter on a field the model lacks is refused before any SQL", func(t *testing.T) {
				_, st := c.call("List", `{"filter":{"nope":{"eq":1}}}`)
				if st == nil || st.Code() != codes.InvalidArgument {
					t.Fatalf("an unknown field answered %v", st)
				}
			})
		})
	}

	// The control on the loop: an engine that stopped running cannot leave this
	// green.
	if engines == 0 {
		t.Fatal("no engine was walked, so this test measured nothing")
	}
}

// A real constraint violation from a real server reaches a gRPC client as a
// code and a machine-readable reason, and as nothing a client could not have
// known.
//
// The unit suite renders the whole captured corpus; this is the live half — the
// error came out of the server running beside this process, over every target
// including the two ORM ones, because there are two independent classifiers and
// the corpus exercises neither of them end to end.
func TestAClassifiedConflictReachesAGrpcClientWithNothingInternal(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	walked := 0
	for _, tg := range egTargets() {
		t.Run(tg.name, func(t *testing.T) {
			walked++
			egWipe(t, tg.source)
			repository := EgConses.Bind(tg.source)
			if err := repository.Save(ctx, &EgCons{Slug: "taken", Tag: crud.Set("t")}); err != nil {
				t.Fatal(err)
			}
			c := grpcServeRepo(t, repository)

			_, st := c.call("Create", `{"Slug":"taken","Tag":"other"}`)
			if st == nil {
				t.Fatal("the database accepted a duplicate unique key")
			}
			if st.Code() != codes.AlreadyExists {
				t.Fatalf("a duplicate unique key answered %s: %s", st.Code(), st.Message())
			}

			said := []string{st.Message()}
			var reason string
			for _, d := range st.Details() {
				switch v := d.(type) {
				case *errdetails.BadRequest:
					for _, fv := range v.GetFieldViolations() {
						said = append(said, fv.GetField(), fv.GetDescription(), fv.GetReason())
					}
				case *errdetails.ErrorInfo:
					said = append(said, v.GetReason(), v.GetDomain())
					reason = v.GetReason()
				}
			}

			// Nothing internal. The names are the ones this schema really has,
			// so a renderer that echoed the driver would be caught by the
			// table, the column or the constraint.
			for what, secret := range map[string]string{
				"the table":         "eg_cons",
				"the column":        "slug",
				"the SQLSTATE":      "23505",
				"the engine number": "1062",
				"the driver text":   "duplicate",
			} {
				for _, s := range said {
					if strings.Contains(strings.ToLower(s), strings.ToLower(secret)) {
						t.Fatalf("the status names %s (%q) in %q", what, secret, s)
					}
				}
			}

			// And it says something: a client that can branch is the whole
			// point, and a renderer answering an empty status would pass every
			// assertion above.
			if st.Message() == "" {
				t.Fatal("the status carries no message")
			}
			if reason == "" {
				t.Fatalf("the status carries no machine-readable reason: %v", st.Details())
			}
			// The finer code only where the adapter names its engine. Through
			// ent it is the coarse one — [[D-046]]'s degradation, and asserting
			// it here is what stops this test from quietly passing on a target
			// that classifies nothing.
			want := string(errs.CodeConflict)
			if tg.classifies {
				want = string(errs.CodeUnique)
			}
			if reason != want {
				t.Fatalf("the reason is %q, want %q on this target", reason, want)
			}

			// The control on the fixture: a row that breaks nothing goes
			// through, so the refusal above came from the constraint rather
			// than from a binding that refuses every write.
			c.ok("Create", `{"Slug":"free","Tag":"t"}`)
		})
	}
	if walked == 0 {
		t.Fatal("no target was walked, so this test measured nothing")
	}
}
