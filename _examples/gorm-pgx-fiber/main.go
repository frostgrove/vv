package main

import (
	"flag"
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly CreatedAt

type Product struct {
	ID        int64         `gorm:"primaryKey" db:"id,pk,auto" json:"id"`
	Sku       string        `gorm:"size:64;unique;not null" db:"sku" json:"sku"`
	Name      string        `gorm:"size:120;not null" db:"name" json:"name"`
	Price     int           `gorm:"not null" db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `gorm:"not null;default:true" db:"active" json:"active"`
	CreatedAt time.Time     `gorm:"not null;default:now()" db:"created_at,generated" json:"createdAt"`
}

func (Product) TableName() string { return "gorm_fiber_products" }

var Products = sqlrepo.Define[Product, int64, ProductUpdate]("gorm_fiber_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	if err := bootstrap(database); err != nil {
		log.Fatal(err)
	}

	repository := specs.Executor(Products.Bind(crudsql.Postgres(sqlDB)))

	app := fiber.New()
	app.Use("/products", crudfiber.New(repository,
		crudfiber.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Routes())

	log.Printf("gorm + crudsql + fiber on %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(app.Listen(*addr))
}

func bootstrap(database *gorm.DB) error {
	if err := database.AutoMigrate(&Product{}); err != nil {
		return err
	}
	if err := database.Exec("DELETE FROM gorm_fiber_products").Error; err != nil {
		return err
	}
	rows := []Product{
		{Sku: "BOLT-1", Name: "hex bolt", Price: 250, Stock: crud.Set(40), Active: true},
		{Sku: "NUT-1", Name: "hex nut", Price: 120, Active: true},
		{Sku: "WSH-1", Name: "washer", Price: 35, Stock: crud.Set(900), Active: true},
	}
	return database.Create(&rows).Error
}
