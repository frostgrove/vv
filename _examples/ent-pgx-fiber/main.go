// Command ent-pgx-fiber is vv with ent as the model source: an ent
// client and a database/sql pool over the same pgx stdlib connection, the
// crudsql adapter, and the Fiber binding.
//
// The point is what does not change. An ent project already owns a generated
// entity struct, a migration and a set of builders; nothing here asks it to
// give any of that up. vv binds to entmodel.Product as generated, and
// the *sql.DB that ent's driver wraps is the same one crudsql serves from —
// one pool, ent migrating and seeding through it, vv reading and writing
// through it.
//
//	go get github.com/frostgrove/vv
//	go get github.com/frostgrove/vv/crud/http/crudfiber
//	go get entgo.io/ent
//	go get github.com/jackc/pgx/v5
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./ent-pgx-fiber
//	curl 'localhost:8080/products?f=price:gte:100&sort=-price'
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/frostgrove/vv/_examples/entmodel"
	"github.com/frostgrove/vv/_examples/entstore"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// The model is ent's own generated struct, entmodel.Product, bound as-is —
// nothing declared here. entstore.ProductUpdate and entstore.Product_ are the
// vv update DTO and metamodel generated from it; see entstore/doc.go for
// how.

// Products is validated when this package initialises: a mistyped tag, a DTO
// field the model lacks or a wrong ID type fails here rather than at request
// time. "products" is ent's own table name for Product — the two clients
// share the table, not just the connection.
var Products = sqlrepo.Define[entmodel.Product, int64, entstore.ProductUpdate]("products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	ctx := context.Background()
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	client := entmodel.NewClient(entmodel.Driver(entsql.OpenDB(dialect.Postgres, database)))
	defer client.Close()
	if err := bootstrap(ctx, client); err != nil {
		log.Fatal(err)
	}

	// crudsql.Postgres wraps the same *sql.DB ent's driver holds — the pool
	// that just migrated and seeded is the pool that serves every request.
	repository := specs.Executor(Products.Bind(crudsql.Postgres(database)))

	app := fiber.New()
	app.Use("/products", crudfiber.New(repository,
		crudfiber.WithQuery[entmodel.Product, int64, entstore.ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
		// ent's Go-side defaults never run on an vv write, and ent's
		// generated struct carries no `db` tags, so there is nowhere to mark
		// created_at as the server's to fill. vv maps it like any other
		// column and writes what the struct holds — the zero time on a create,
		// and the column's DEFAULT never fires because the INSERT names it.
		// This is the second of the three ways out that
		// docs/usage-guides/ent.md §16 lists, and the only one available when
		// the model is generated code you must not edit.
		crudfiber.BeforeSave[entmodel.Product, int64, entstore.ProductUpdate](
			func(_ fiber.Ctx, p *entmodel.Product) error {
				if p.CreatedAt.IsZero() {
					p.CreatedAt = time.Now()
				}
				return nil
			}),
	).Routes())

	log.Printf("ent + crudsql + fiber on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(app.Listen(*addr))
}

// bootstrap runs ent's own migration and seeds through ent's own builders, so
// the example runs against an empty database. A real application would use
// its own migrations; the point here is that vv takes no part in either
// step and needs none.
func bootstrap(ctx context.Context, client *entmodel.Client) error {
	if err := client.Schema.Create(ctx); err != nil {
		return err
	}
	if _, err := client.Product.Delete().Exec(ctx); err != nil {
		return err
	}
	stock := 40
	if _, err := client.Product.Create().
		SetSku("BOLT-1").SetName("hex bolt").SetPrice(250).SetStock(stock).SetActive(true).
		Save(ctx); err != nil {
		return err
	}
	if _, err := client.Product.Create().
		SetSku("NUT-1").SetName("hex nut").SetPrice(120).SetActive(true).
		Save(ctx); err != nil {
		return err
	}
	stock = 900
	if _, err := client.Product.Create().
		SetSku("WSH-1").SetName("washer").SetPrice(35).SetStock(stock).SetActive(true).
		Save(ctx); err != nil {
		return err
	}
	return nil
}
