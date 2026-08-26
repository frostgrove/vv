// Command sql-nethttp is vv with nothing but the standard library on top:
// database/sql, the crudsql adapter, and net/http's own ServeMux. No ORM and no
// web framework.
//
// It is the cheapest stack there is, and not only in dependencies: the crudnet
// binding imports nothing outside the standard library, so it ships inside the
// library's own module. There is no second `go get` for the transport the way
// there is for Fiber or Gin, and no framework in anybody's build.
//
//	go get github.com/frostgrove/vv
//	go get github.com/jackc/pgx/v5
//
// The driver is the only other line, and only because PostgreSQL needs one.
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./sql-nethttp
//	curl 'localhost:8080/products?f=price:gte:100&sort=-price'
package main

import (
	"database/sql"
	"flag"
	"log"
	"net/http"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/utils/vvdb"
)

//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly CreatedAt

// Product is the model: a plain struct with `db` tags, the same shape every
// other example serves.
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
var Products = sqlrepo.Define[Product, int64, ProductUpdate]("sql_nethttp_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

// The database, described once. vvdb builds the string and sizes the pool; the
// driver is the blank import above, which is why vvdb needs no dependency of
// its own.
var database = vvdb.Config{
	Engine: vvdb.Postgres, Host: "localhost", Port: 55432,
	User: "vv", Password: "vv", Name: "vv", SSLMode: "disable",
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	db, err := vvdb.Open(database)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := bootstrap(db); err != nil {
		log.Fatal(err)
	}

	repo := specs.Executor(Products.Bind(crudsql.Postgres(db)))

	mux := http.NewServeMux()
	crudnet.New(repo,
		crudnet.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Mount(mux, "/products")

	log.Printf("database/sql + crudsql + net/http on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// bootstrap creates the table and seeds it, so the example runs against an
// empty database. A real application would use its own migrations.
func bootstrap(db *sql.DB) error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS sql_nethttp_products`,
		`CREATE TABLE sql_nethttp_products (
			id         BIGSERIAL PRIMARY KEY,
			sku        TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL,
			price      INT NOT NULL,
			stock      INT,
			active     BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO sql_nethttp_products (sku, name, price, stock) VALUES
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
