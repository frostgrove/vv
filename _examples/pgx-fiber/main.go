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

type Product struct {
	ID        int64         `db:"id,pk,auto" json:"id"`
	Sku       string        `db:"sku" json:"sku"`
	Name      string        `db:"name" json:"name"`
	Price     int           `db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `db:"active" json:"active"`
	CreatedAt time.Time     `db:"created_at,generated" json:"createdAt"`
}

var Products = sqlrepo.Define[Product, int64, ProductUpdate]("pgx_fiber_products",
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

	ctx := context.Background()

	pool, err := dbpgx.Connect(ctx, &database)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := bootstrap(ctx, pool); err != nil {
		log.Fatal(err)
	}

	repository := specs.Executor(Products.Bind(crudpgx.Open(pool)))

	app := fiber.New()
	app.Use("/products", crudfiber.New(repository,
		crudfiber.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Routes())

	log.Printf("pgx + crudpgx + fiber on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(app.Listen(*addr))
}

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
