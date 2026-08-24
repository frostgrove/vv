// Command sqlx-pgx-gin is rx-crud with sqlx over pgx, and the Gin binding.
//
// sqlx and rx-crud both read struct tags to map Go fields onto columns, and
// they read the same tag: `db`. There is one tag set on Product, not two —
// sqlx's own tag conventions and rx-crud's `pk`/`auto`/`generated` markers
// live side by side in the same `db:"..."` string. sqlx keeps the job it is
// good at, the connection and the bootstrap DDL; rx-crud serves the CRUD
// surface the handler exposes, because crud.Opt[int] is not a shape sqlx's
// struct scanner understands.
//
//	go get github.com/shardit-io/vv
//	go get github.com/shardit-io/vv/adapter/crudsql
//	go get github.com/shardit-io/vv/http/crudgin
//	go get github.com/jmoiron/sqlx
//	go get github.com/jackc/pgx/v5
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./sqlx-pgx-gin
//	curl 'localhost:8080/products?f=price:gte:100&sort=-price'
package main

import (
	"flag"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/shardit-io/vv/adapter/crudsql"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/http/crudgin"
	"github.com/shardit-io/vv/query"
	"github.com/shardit-io/vv/repo/basic"
	"github.com/shardit-io/vv/repo/decorators/specs"
)

//go:generate go run github.com/shardit-io/vv/cmd/rxcrud -readonly CreatedAt

// Product carries one tag set, `db`, read by both sqlx and rx-crud: sqlx maps
// columns onto fields with it, and rx-crud reads the same tag for the pk,
// auto and generated markers. Two libraries, one struct, no duplication.
type Product struct {
	ID        int64         `db:"id,pk,auto" json:"id"`
	Sku       string        `db:"sku" json:"sku"`
	Name      string        `db:"name" json:"name"`
	Price     int           `db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `db:"active" json:"active"`
	CreatedAt time.Time     `db:"created_at,generated" json:"createdAt"`
}

// Products is validated when this package initialises: a mistyped tag, a DTO
// field the model lacks or a wrong ID type fails here rather than at request
// time.
var Products = basic.Define[Product, int64, ProductUpdate]("sqlx_gin_products",
	basic.DefaultLimit(20),
	basic.MaxLimit(100),
	basic.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://rxcrud:rxcrud@localhost:55432/rxcrud?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := bootstrap(db); err != nil {
		log.Fatal(err)
	}

	// sqlx.DB embeds the *sql.DB rx-crud needs, so the same pool serves both:
	// sqlx for the bootstrap statements below, crudsql for everything the API
	// serves.
	repo := specs.Executor(Products.Bind(crudsql.Postgres(db.DB)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	crudgin.New(repo,
		crudgin.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Mount(r, "/products")

	log.Printf("sqlx + crudsql (pgx) + gin on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(r.Run(*addr))
}

// bootstrap creates the table and seeds it, so the example runs against an
// empty database. A real application would use its own migrations.
func bootstrap(db *sqlx.DB) error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS sqlx_gin_products`,
		`CREATE TABLE sqlx_gin_products (
			id         BIGSERIAL PRIMARY KEY,
			sku        TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL,
			price      INT NOT NULL,
			stock      INT,
			active     BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO sqlx_gin_products (sku, name, price, stock) VALUES
			('BOLT-1', 'hex bolt', 250, 40),
			('NUT-1',  'hex nut',  120, NULL),
			('WSH-1',  'washer',    35, 900)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
