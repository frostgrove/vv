// Command gorm-pgx-fiber is vv with gorm: one struct carries both gorm's
// tags and vv's `db` tags, gorm owns migrations and seeding, vv
// serves reads and writes over the same *sql.DB.
//
// This is what an existing gorm project actually does to adopt vv: add
// tags to the model it already has, nothing more. It also shows the pool is
// shared, not duplicated — gorm's AutoMigrate and vv's queries run
// through the same underlying *sql.DB, so a gorm transaction and an vv
// call can be the same transaction.
//
//	go get github.com/shardit-io/vv
//	go get github.com/shardit-io/vv/crud/http/crudfiber
//	go get gorm.io/gorm
//	go get gorm.io/driver/postgres
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./gorm-pgx-fiber
//	curl 'localhost:8080/products?f=price:gte:100&sort=-price'
package main

import (
	"flag"
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/adapter/crudsql"
	"github.com/shardit-io/vv/crud/decorators/specs"
	"github.com/shardit-io/vv/crud/http/crudfiber"
	"github.com/shardit-io/vv/crud/query"
	"github.com/shardit-io/vv/crud/sqlrepo"
)

//go:generate go run github.com/shardit-io/vv/cmd/vv -readonly CreatedAt

// Product is an ordinary gorm model. The `gorm` tags are what the project
// already had; the `db` tags are the only addition vv needs, sitting on
// the same fields rather than a parallel struct.
type Product struct {
	ID        int64         `gorm:"primaryKey" db:"id,pk,auto" json:"id"`
	Sku       string        `gorm:"size:64;unique;not null" db:"sku" json:"sku"`
	Name      string        `gorm:"size:120;not null" db:"name" json:"name"`
	Price     int           `gorm:"not null" db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `gorm:"not null;default:true" db:"active" json:"active"`
	CreatedAt time.Time     `gorm:"not null;default:now()" db:"created_at,generated" json:"createdAt"`
}

// TableName keeps this example off the table names the other examples use, so
// they can all run against the same database without colliding.
func (Product) TableName() string { return "gorm_fiber_products" }

// Products is validated when this package initialises: a mistyped tag, a DTO
// field the model lacks or a wrong ID type fails here rather than at request
// time.
var Products = sqlrepo.Define[Product, int64, ProductUpdate]("gorm_fiber_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	if err := bootstrap(db); err != nil {
		log.Fatal(err)
	}

	// crudsql.Postgres wraps the same *sql.DB gorm holds — one pool, two
	// libraries. No connection or transaction changes hands, so a caller free
	// to reach in with crud.WithExecutor can put a gorm transaction and an
	// vv call in the same one.
	repo := specs.Executor(Products.Bind(crudsql.Postgres(sqlDB)))

	app := fiber.New()
	app.Use("/products", crudfiber.New(repo,
		crudfiber.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Routes())

	log.Printf("gorm + crudsql + fiber on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(app.Listen(*addr))
}

// bootstrap lets gorm own the schema, the way it already does in a project
// that has not adopted vv. A real application would use its own
// migration tool instead of AutoMigrate; the delete-then-seed keeps this
// example idempotent since AutoMigrate never drops rows on its own.
func bootstrap(db *gorm.DB) error {
	if err := db.AutoMigrate(&Product{}); err != nil {
		return err
	}
	if err := db.Exec("DELETE FROM gorm_fiber_products").Error; err != nil {
		return err
	}
	rows := []Product{
		{Sku: "BOLT-1", Name: "hex bolt", Price: 250, Stock: crud.Set(40), Active: true},
		{Sku: "NUT-1", Name: "hex nut", Price: 120, Active: true},
		{Sku: "WSH-1", Name: "washer", Price: 35, Stock: crud.Set(900), Active: true},
	}
	return db.Create(&rows).Error
}
