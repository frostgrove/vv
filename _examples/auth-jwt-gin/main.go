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

type Note struct {
	ID        int64     `db:"id,pk,auto" json:"id"`
	TenantID  int64     `db:"tenant_id" json:"tenantId"`
	Author    string    `db:"author" json:"author"`
	Title     string    `db:"title" json:"title"`
	Body      string    `db:"body" json:"body"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

type NoteUpdate struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

var Notes = sqlrepo.Define[Note, int64, NoteUpdate]("auth_jwt_gin_notes",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

var roles = auth.RoleMap{
	"editor": {"note:read", "note:write", "note:delete"},
	"reader": {"note:read"},
}

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

	guard := auth.NewGuard(authjwt.Standard(
		authjwt.HMAC(secret), roles,
		authjwt.Issuer(issuer),
		authjwt.Audience(audience),
		authjwt.Leeway(30*time.Second),
	))

	repository := specs.Executor(Notes.Bind(crudpgx.Open(pool), security.Gate(policy)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(crudgin.Errors(), authgin.Middleware(guard))
	crudgin.New(repository,
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
