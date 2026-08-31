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

type Product struct {
	ID        int64         `db:"id,pk,auto" json:"id"`
	Sku       string        `db:"sku" json:"sku"`
	Name      string        `db:"name" json:"name"`
	Price     int           `db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `db:"active" json:"active"`
	CreatedAt time.Time     `db:"created_at,generated" json:"createdAt"`
}

var Products = sqlrepo.Define[Product, int64, ProductUpdate]("sql_nethttp_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

var database = vvdb.Config{
	Engine: vvdb.Postgres, Host: "localhost", Port: 55432,
	User: "vv", Password: "vv", Name: "vv", SSLMode: "disable",
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	database, err := vvdb.Open(&database)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if err := bootstrap(database); err != nil {
		log.Fatal(err)
	}

	repository := specs.Executor(Products.Bind(crudsql.Postgres(database)))

	mux := http.NewServeMux()
	crudnet.New(repository,
		crudnet.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Mount(mux, "/products")

	log.Printf("database/sql + crudsql + net/http on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func bootstrap(database *sql.DB) error {
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
		if _, err := database.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
