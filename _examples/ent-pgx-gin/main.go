package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/frostgrove/vv/_examples/entmodel"
	"github.com/frostgrove/vv/_examples/entstore"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

var Products = sqlrepo.Define[entmodel.Product, int64, entstore.ProductUpdate]("products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	database, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	ctx := context.Background()
	if err := bootstrap(ctx, database); err != nil {
		log.Fatal(err)
	}

	repository := specs.Executor(Products.Bind(crudsql.Postgres(database)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	crudgin.New(repository,
		crudgin.WithQuery[entmodel.Product, int64, entstore.ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),

		crudgin.BeforeSave[entmodel.Product, int64, entstore.ProductUpdate](
			func(_ *gin.Context, p *entmodel.Product) error {
				if p.CreatedAt.IsZero() {
					p.CreatedAt = time.Now()
				}
				return nil
			}),
	).Mount(r, "/products")

	log.Printf("ent + crudsql + gin on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(r.Run(*addr))
}

func bootstrap(ctx context.Context, database *sql.DB) error {
	client := entmodel.NewClient(entmodel.Driver(entsql.OpenDB("postgres", database)))

	if err := client.Schema.Create(ctx); err != nil {
		return err
	}
	if _, err := client.Product.Delete().Exec(ctx); err != nil {
		return err
	}

	stock := 40
	if _, err := client.Product.Create().SetSku("BOLT-1").SetName("hex bolt").SetPrice(250).SetStock(stock).Save(ctx); err != nil {
		return err
	}
	if _, err := client.Product.Create().SetSku("NUT-1").SetName("hex nut").SetPrice(120).Save(ctx); err != nil {
		return err
	}
	stock = 900
	if _, err := client.Product.Create().SetSku("WSH-1").SetName("washer").SetPrice(35).SetStock(stock).Save(ctx); err != nil {
		return err
	}
	return nil
}
