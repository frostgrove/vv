package main

import (
	"flag"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
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

var Products = sqlrepo.Define[Product, int64, ProductUpdate]("sqlx_gin_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	database, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if err := bootstrap(database); err != nil {
		log.Fatal(err)
	}

	repository := specs.Executor(Products.Bind(crudsql.Postgres(database.DB)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	crudgin.New(repository,
		crudgin.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Mount(r, "/products")

	log.Printf("sqlx + crudsql (pgx) + gin on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(r.Run(*addr))
}

func bootstrap(database *sqlx.DB) error {
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
		if _, err := database.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
