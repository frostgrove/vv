// Command auth-jwt-gin is the whole authentication and authorization chain in
// one file: a JWT at the door, a principal in the context, and a tenant filter
// in the SQL.
//
// It is the example that shows why the pieces are separate. The middleware
// knows nothing about tenants; the policy knows nothing about JWTs; the model
// knows about neither. What connects them is one context value and one claim
// name.
//
//	go get github.com/frostgrove/vv
//	go get github.com/frostgrove/vv/crud/adapter/crudpgx
//	go get github.com/frostgrove/vv/crud/http/crudgin
//	go get github.com/frostgrove/vv/auth/http/authgin
//	go get github.com/frostgrove/vv/auth/authjwt
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./auth-jwt-gin
//
// It prints three tokens on start-up. What they demonstrate:
//
//	curl -s localhost:8080/notes
//	  401 — no credential
//
//	curl -s -H "Authorization: Bearer $EDITOR1" localhost:8080/notes
//	  only tenant 1's rows, filtered in SQL
//
//	curl -s -H "Authorization: Bearer $EDITOR1" localhost:8080/notes/<tenant-2-id>
//	  404, not 403 — a denial would confirm the row exists (D-008)
//
//	curl -s -X DELETE -H "Authorization: Bearer $READER1" localhost:8080/notes/<own-id>
//	  403 — authenticated, but the role grants no delete
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/frostgrove/vv/auth/http/authgin"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// Note is the model. TenantID is an ordinary column — nothing marks it as the
// tenant, because the policy says that and the model does not need to know.
type Note struct {
	ID        int64     `db:"id,pk,auto" json:"id"`
	TenantID  int64     `db:"tenant_id" json:"tenantId"`
	Author    string    `db:"author" json:"author"`
	Title     string    `db:"title" json:"title"`
	Body      string    `db:"body" json:"body"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

// NoteUpdate is the patch DTO. TenantID is absent on purpose: the policy
// freezes it anyway, and a field that cannot be written has no business being
// offered.
type NoteUpdate struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

var Notes = sqlrepo.Define[Note, int64, NoteUpdate]("auth_jwt_gin_notes",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

// roles is the whole permission model, and it is a value rather than a
// registry: two libraries declaring "editor" differently must not settle it by
// link order.
var roles = auth.RoleMap{
	"editor": {"note:read", "note:write", "note:delete"},
	"reader": {"note:read"},
}

// policy is where identity becomes SQL. Three independent rules, ANDed:
//
//   - PerAction: what this caller may do at all. A verb the map does not name
//     is refused, so a verb added to the library later stays refused until
//     somebody grants it.
//   - ScopeAttr: which rows exist for them. The "tenant" claim becomes a WHERE
//     clause on every read, and — because ScopeAttr wraps ScopeField — a create
//     into another tenant is refused and tenant_id is frozen against updates.
//   - ScopeSubject on Author is deliberately *not* here: a tenant's editors
//     share their notes. Adding it would narrow to the caller's own rows.
var policy = security.Combine(
	security.PerAction[Note, int64](map[security.Action]auth.Permission{
		security.Read:   "note:read",
		security.Create: "note:write",
		security.Update: "note:write",
		security.Delete: "note:delete",
	}),
	security.ScopeAttr[Note, int64]("TenantID", "tenant"),
)

const (
	dsn      = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"
	issuer   = "https://id.example.com"
	audience = "notes-api"
)

// secret stands in for what a real deployment reads from its environment. HMAC
// is the symmetric case: whatever can verify can also sign, so a service that
// does not issue its own tokens wants authjwt.RSA or authjwt.JWKS instead.
var secret = []byte("an example secret, long enough to be one")

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := bootstrap(ctx, pool); err != nil {
		log.Fatal(err)
	}

	// The guard is built once. Issuer and Audience are not optional: New panics
	// without them or without the waiver that says they are deliberately
	// unchecked, so a parser that would over-trust never reaches a request.
	guard := auth.NewGuard(authjwt.Standard(
		authjwt.HMAC(secret), roles,
		authjwt.Issuer(issuer),
		authjwt.Audience(audience),
		authjwt.Leeway(30*time.Second),
	))

	// The gate goes on the repository, not on the route. That is the whole
	// point: every entry point this repository has is covered, including the
	// ones a handler does not spell out.
	repo := specs.Executor(Notes.Bind(crudpgx.Open(pool), security.Gate(policy)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(crudgin.Errors(), authgin.Middleware(guard))
	crudgin.New(repo,
		crudgin.WithQuery[Note, int64, NoteUpdate](&query.Config{
			Filterable: []string{"Author", "Title", "CreatedAt"},
			Sortable:   []string{"Title", "CreatedAt"},
			Searchable: []string{"Title", "Body"},
		}),
	).Mount(r, "/notes")

	printTokens()
	log.Printf("jwt + gin + tenant scope on %s", *addr)
	log.Fatal(r.Run(*addr))
}

// printTokens mints the three credentials the header comment uses. A real
// service does not issue its own tokens; this is the identity provider the
// example does not have.
func printTokens() {
	for _, who := range []struct {
		name, sub, role string
		tenant          int64
	}{
		{"EDITOR1", "alice", "editor", 1},
		{"READER1", "bob", "reader", 1},
		{"EDITOR2", "carol", "editor", 2},
	} {
		tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":    who.sub,
			"iss":    issuer,
			"aud":    audience,
			"exp":    time.Now().Add(8 * time.Hour).Unix(),
			"roles":  []string{who.role},
			"tenant": who.tenant,
		}).SignedString(secret)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%s=%s", who.name, tok)
	}
}

// bootstrap creates the table and seeds two tenants, so the filtering is
// visible. A real application would use its own migrations.
func bootstrap(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS auth_jwt_gin_notes`,
		`CREATE TABLE auth_jwt_gin_notes (
			id         BIGSERIAL PRIMARY KEY,
			tenant_id  BIGINT NOT NULL,
			author     TEXT NOT NULL,
			title      TEXT NOT NULL,
			body       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`INSERT INTO auth_jwt_gin_notes (tenant_id, author, title, body) VALUES
			(1, 'alice', 'first tenant, first note',  'visible to tenant 1'),
			(1, 'bob',   'first tenant, second note', 'also visible to tenant 1'),
			(2, 'carol', 'second tenant',             'visible to tenant 2 only')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
