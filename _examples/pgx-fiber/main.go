// Command pgx-fiber is vv with no ORM at all: a pgx v5 pool, the crudpgx
// adapter, and the Fiber binding.
//
// This is the shortest path there is. There is no ORM to adopt and no
// database/sql in the way — vv talks to the pool directly, and the model
// is an ordinary struct with `db` tags.
//
//	go get github.com/frostgrove/vv
//	go get github.com/frostgrove/vv/crud/adapter/crudpgx
//	go get github.com/frostgrove/vv/utils/vvdb/dbpgx
//	go get github.com/frostgrove/vv/crud/http/crudfiber
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./pgx-fiber
//	curl 'localhost:8080/products?f=price:gte:100&sort=-price'
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/frostgrove/vv/utils/vvdb/dbpgx"
)

//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly CreatedAt

// Product is the model: a plain struct, `db` tags, nothing generated and
// nothing embedded. `auto` says the database owns the key, so a create request
// cannot choose one; `generated` says the same about the timestamp.
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
var Products = sqlrepo.Define[Product, int64, ProductUpdate]("pgx_fiber_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

// The database, described once. The same struct is what vvcfg loads out of a
// YAML file in a real service — this one is a literal so the example stays a
// single file.
var database = vvdb.Config{
	Engine: vvdb.Postgres, Host: "localhost", Port: 55432,
	User: "vv", Password: "vv", Name: "vv", SSLMode: "disable",
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	ctx := context.Background()
	// dbpgx builds the connection string and sizes the pool; the application
	// owns what comes back, and hands it to vv on the crudpgx line below.
	pool, err := dbpgx.Connect(ctx, database)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := bootstrap(ctx, pool); err != nil {
		log.Fatal(err)
	}

	// crudpgx.Open is the whole adapter: vv asks the pool to run a
	// statement and to give back rows, and never opens a connection of its own.
	repo := specs.Executor(Products.Bind(crudpgx.Open(pool)))

	app := fiber.New()
	app.Use("/products", crudfiber.New(repo,
		crudfiber.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Routes())

	log.Printf("pgx + crudpgx + fiber on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(app.Listen(*addr))
}

// bootstrap creates the table and seeds it, so the example runs against an
// empty database. A real application would use its own migrations.
//
// The `active` default is worth a word, because the responses show it. vv
// writes every mapped column, so an INSERT it builds names `active` and the
// column DEFAULT never fires: create a product without one and it is stored
// false, not true. A column default only reaches rows the database makes on its
// own. Where the server, not the client, must own a value, mark the column
// `generated` — as `created_at` is here — or fill it in a BeforeSave hook.
func bootstrap(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS pgx_fiber_products`,
		`CREATE TABLE pgx_fiber_products (
			id         BIGSERIAL PRIMARY KEY,
			sku        TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL,
			price      INT NOT NULL,
			stock      INT,
			active     BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO pgx_fiber_products (sku, name, price, stock) VALUES
			('BOLT-1', 'hex bolt', 250, 40),
			('NUT-1',  'hex nut',  120, NULL),
			('WSH-1',  'washer',    35, 900)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
