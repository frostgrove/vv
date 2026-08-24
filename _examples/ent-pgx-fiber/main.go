// Command ent-pgx-fiber is rx-crud with ent as the model source: an ent
// client and a database/sql pool over the same pgx stdlib connection, the
// crudsql adapter, and the Fiber binding.
//
// The point is what does not change. An ent project already owns a generated
// entity struct, a migration and a set of builders; nothing here asks it to
// give any of that up. rx-crud binds to entmodel.Product as generated, and
// the *sql.DB that ent's driver wraps is the same one crudsql serves from —
// one pool, ent migrating and seeding through it, rx-crud reading and writing
// through it.
//
//	go get github.com/shardit-io/go-rx-crud
//	go get github.com/shardit-io/go-rx-crud/http/crudfiber
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

	"github.com/shardit-io/go-rx-crud/_examples/entmodel"
	"github.com/shardit-io/go-rx-crud/_examples/entstore"
	"github.com/shardit-io/go-rx-crud/adapter/crudsql"
	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/http/crudfiber"
	"github.com/shardit-io/go-rx-crud/query"
	"github.com/shardit-io/go-rx-crud/repo/basic"
	"github.com/shardit-io/go-rx-crud/repo/decorators/specs"
)

// The model is ent's own generated struct, entmodel.Product, bound as-is —
// nothing declared here. entstore.ProductUpdate and entstore.Product_ are the
// rx-crud update DTO and metamodel generated from it; see entstore/doc.go for
// how.

// Products is validated when this package initialises: a mistyped tag, a DTO
// field the model lacks or a wrong ID type fails here rather than at request
// time. "products" is ent's own table name for Product — the two clients
// share the table, not just the connection.
var Products = basic.Define[entmodel.Product, int64, entstore.ProductUpdate]("products",
	basic.DefaultLimit(20),
	basic.MaxLimit(100),
	basic.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://rxcrud:rxcrud@localhost:55432/rxcrud?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	client := entmodel.NewClient(entmodel.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	if err := bootstrap(ctx, client); err != nil {
		log.Fatal(err)
	}

	// crudsql.Postgres wraps the same *sql.DB ent's driver holds — the pool
	// that just migrated and seeded is the pool that serves every request.
	repo := specs.Executor(Products.Bind(crudsql.Postgres(db)))

	app := fiber.New()
	app.Use("/products", crudfiber.New(repo,
		crudfiber.WithQuery[entmodel.Product, int64, entstore.ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
		// ent's Go-side defaults never run on an rx-crud write, and ent's
		// generated struct carries no `db` tags, so there is nowhere to mark
		// created_at as the server's to fill. rx-crud maps it like any other
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
// its own migrations; the point here is that rx-crud takes no part in either
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
