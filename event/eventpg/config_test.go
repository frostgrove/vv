package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Nothing listens there, and sql.Open reaches it only when a statement does.
const nowhereDSN = "postgres://nobody:nobody@127.0.0.1:1/nowhere?sslmode=disable&connect_timeout=1"

func handle(t *testing.T) (*sql.DB, crud.Source) {
	t.Helper()
	db, err := sql.Open("pgx", nowhereDSN)
	if err != nil {
		t.Fatalf("a pool over a DSN nothing answers could not be built, so no spec below was assembled: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, crudsql.Postgres(db)
}

func TestNewRefusesEverySpecItCannotAssemble(t *testing.T) {
	db, source := handle(t)
	other, otherSource := handle(t)

	for _, refusal := range []struct {
		what  string
		spec  Spec
		names []string
	}{
		{
			what:  "no database",
			spec:  Spec{Source: source},
			names: []string{"Spec.DB"},
		},
		{
			what:  "no source",
			spec:  Spec{DB: db},
			names: []string{"Spec.Source", "transaction"},
		},
		{
			what:  "a source that reads another database",
			spec:  Spec{DB: db, Source: otherSource},
			names: []string{"Spec.Source", "Spec.DB"},
		},
		{
			what:  "a schema management nobody declared",
			spec:  Spec{DB: db, Source: source, SchemaManagement: SchemaManagement(7)},
			names: []string{"[schema management 7]", "[schema management verify]", "[schema management manage]"},
		},
		{
			what:  "a schema name PostgreSQL would have to quote",
			spec:  Spec{DB: db, Source: source, Schema: Schema{Name: "Events"}},
			names: []string{`"Events"`, "[a-z][a-z0-9_]*"},
		},
		{
			what:  "a payload bound over the kernel ceiling",
			spec:  Spec{DB: db, Source: source, Schema: Schema{MaxPayload: event.MaxPayloadBytes + 1}},
			names: []string{"Schema.MaxPayload", "1048577", "1048576"},
		},
		{
			what:  "a key bound over the kernel ceiling",
			spec:  Spec{DB: db, Source: source, Schema: Schema{MaxKey: event.MaxKeyBytes + 1}},
			names: []string{"Schema.MaxKey", "2049", "2048"},
		},
		{
			what:  "a batch over the kernel ceiling",
			spec:  Spec{DB: db, Source: source, MaxBatch: event.MaxBatchCount + 1},
			names: []string{"Spec.MaxBatch", "1025", "1024"},
		},
		{
			what:  "a stream page over the kernel ceiling",
			spec:  Spec{DB: db, Source: source, StreamPage: event.MaxPageCount + 1},
			names: []string{"Spec.StreamPage", "4097", "4096"},
		},
		{
			what:  "a read page over what one read may hold",
			spec:  Spec{DB: db, Source: source, Schema: Schema{MaxPayload: 1 << 20}, MaxRead: 200},
			names: []string{"Spec.MaxRead", "200", "1048576", "64"},
		},
	} {
		t.Run(refusal.what, func(t *testing.T) {
			store, err := New(refusal.spec)
			if store != nil {
				t.Fatal("the spec was refused and a store was answered anyway, so a composition root would serve from it")
			}
			if !errors.Is(err, ErrSpec) {
				t.Fatalf("the refusal is %v, and a wiring error a composition root branches on is %v", err, ErrSpec)
			}
			for _, named := range refusal.names {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("the refusal does not say %q, so whoever reads it at start-up learns that something is wrong and not what: %v", named, err)
				}
			}
		})
	}

	t.Run("a legal spec is accepted and answers what it resolved to", func(t *testing.T) {
		store, err := New(Spec{DB: db, Source: source})
		if err != nil {
			t.Fatalf("a spec naming a pool and its own source was refused, so every refusal above proves nothing: %v", err)
		}
		if store.Schema() != (Schema{Name: DefaultSchema, MaxPayload: DefaultMaxPayload, MaxKey: DefaultMaxKey}) {
			t.Fatalf("the zero Schema resolved to %+v", store.Schema())
		}
		if store.SchemaManagement() != VerifySchema {
			t.Fatalf("an unset schema management resolved to %s, and nothing migrates unless a deployment asked for it", store.SchemaManagement())
		}
		if store.Limits() != (event.Limits{MaxPayload: DefaultMaxPayload, MaxBatch: DefaultMaxBatch, MaxKey: DefaultMaxKey, StreamPage: DefaultPage, MaxRead: DefaultPage}) {
			t.Fatalf("the default limits are %+v", store.Limits())
		}
		if store.Capabilities() != (event.Capabilities{
			Transactions:       event.Supported,
			Persistence:        event.Supported,
			MonotoneVisibility: event.Unsupported,
			SharedBacking:      event.Supported,
		}) {
			t.Fatalf("the capabilities are %+v", store.Capabilities())
		}
	})

	t.Run("a payload bound narrows the pages the store publishes", func(t *testing.T) {
		store, err := New(Spec{DB: other, Source: otherSource, Schema: Schema{MaxPayload: 1 << 20}})
		if err != nil {
			t.Fatalf("a store at the kernel's own payload ceiling was refused: %v", err)
		}
		resident := event.ResidentPage(1 << 20)
		if store.Limits().StreamPage != resident || store.Limits().MaxRead != resident {
			t.Fatalf("the pages are %d and %d at a payload bound of 1 MiB, and %d envelopes is what one read may hold",
				store.Limits().StreamPage, store.Limits().MaxRead, resident)
		}
	})
}

func TestTwoStoreValuesOverOneSchemaAreOneStoreAndTwoSchemasAreNot(t *testing.T) {
	db, source := handle(t)
	other, otherSource := handle(t)

	over := func(t *testing.T, db *sql.DB, source crud.Source, schema string) *Store {
		t.Helper()
		store, err := New(Spec{DB: db, Source: source, Schema: Schema{Name: schema}})
		if err != nil {
			t.Fatalf("a store over %s was refused: %v", schema, err)
		}
		return store
	}

	first := over(t, db, source, DefaultSchema)
	second := over(t, db, source, DefaultSchema)
	beside := over(t, db, source, "other_events")
	elsewhere := over(t, other, otherSource, DefaultSchema)

	if !first.Backing().Equal(second.Backing()) || !second.Backing().Equal(first.Backing()) {
		t.Fatal("two store values over one schema of one database are not one store, so a cursor one of them minted is refused by the other")
	}
	if first.Backing().Equal(beside.Backing()) || beside.Backing().Equal(first.Backing()) {
		t.Fatal("two schemas of one database are one backing, so a cursor minted over one of them is accepted by the other and the walk resumes at a position of somebody else's log")
	}
	if first.Backing().Equal(elsewhere.Backing()) || elsewhere.Backing().Equal(first.Backing()) {
		t.Fatal("one schema name in two databases is one backing, so a cursor travels between them")
	}
}

func TestNewAgainstADeadDSNAnswersAStore(t *testing.T) {
	db, source := handle(t)

	t.Run("the pool really does reach nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err == nil {
			t.Fatal("something answered at the DSN this test needs nothing to answer at, so New performing I/O would have gone unnoticed")
		}
	})

	store, err := New(Spec{DB: db, Source: source, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatalf("New reached the database it was handed, so a composition root cannot assemble its graph before the database is up: %v", err)
	}
	if store.Schema().Name != DefaultSchema || store.SchemaManagement() != ManageSchema {
		t.Fatalf("the store answers %+v and %s", store.Schema(), store.SchemaManagement())
	}
	if store.Limits().MaxPayload != DefaultMaxPayload || !store.Backing().Equal(store.Backing()) {
		t.Fatal("a store built against a database nothing answers cannot answer the three pure questions")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close refused: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("the second Close refused, and closing twice is what a composition root shutting down twice does: %v", err)
	}
}
