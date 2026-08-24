// Command gorm-mysql-gin is rx-crud with gorm as the model layer, MySQL as the
// engine and the Gin binding.
//
// This is the same declaration as the other examples, run against a database
// that has no RETURNING clause: rx-crud reads the written row back by primary
// key instead of asking the statement for it — see [[D-019]]. The caller
// cannot tell the difference; Save and Create both hand back the full row
// either way. The model is an ordinary gorm struct that also carries `db`
// tags, so one type serves both gorm and rx-crud without an adapter struct in
// between.
//
//	go get github.com/shardit-io/go-rx-crud
//	go get github.com/shardit-io/go-rx-crud/http/crudgin
//	go get gorm.io/gorm
//	go get gorm.io/driver/mysql
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./gorm-mysql-gin
//	curl 'localhost:8080/products?f=price:gte:100&sort=-price'
package main

import (
	"flag"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/shardit-io/go-rx-crud/adapter/crudsql"
	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/http/crudgin"
	"github.com/shardit-io/go-rx-crud/query"
	"github.com/shardit-io/go-rx-crud/repo/basic"
	"github.com/shardit-io/go-rx-crud/repo/decorators/specs"
)

//go:generate go run github.com/shardit-io/go-rx-crud/cmd/rxcrud -readonly CreatedAt

// Product is an ordinary gorm model carrying `db` tags alongside the `gorm`
// ones — gorm owns migration and seeding, rx-crud owns the CRUD API, and
// neither reads the other's tag. The unique index needs a bounded size:
// MySQL cannot index an unbounded TEXT column.
type Product struct {
	ID        int64         `gorm:"primaryKey" db:"id,pk,auto" json:"id"`
	Sku       string        `gorm:"size:64;uniqueIndex" db:"sku" json:"sku"`
	Name      string        `gorm:"size:200" db:"name" json:"name"`
	Price     int           `db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `db:"active" json:"active"`
	CreatedAt time.Time     `gorm:"autoCreateTime;default:CURRENT_TIMESTAMP(3)" db:"created_at,generated" json:"createdAt"`
}

// TableName pins the table gorm migrates and rx-crud queries to the same
// name; without it gorm would pluralise the type name instead.
func (Product) TableName() string { return "gorm_mysql_products" }

// Products is validated when this package initialises: a mistyped tag, a DTO
// field the model lacks or a wrong ID type fails here rather than at request
// time.
var Products = basic.Define[Product, int64, ProductUpdate]("gorm_mysql_products",
	basic.DefaultLimit(20),
	basic.MaxLimit(100),
	basic.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "rxcrud:rxcrud@tcp(localhost:53306)/rxcrud?parseTime=true&loc=UTC"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
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

	// crudsql.MySQL wraps the *sql.DB gorm already opened. rx-crud sends its
	// own statements over it; gorm's callback chain never runs on them.
	repo := specs.Executor(Products.Bind(crudsql.MySQL(sqlDB)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	crudgin.New(repo,
		crudgin.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Mount(r, "/products")

	log.Printf("gorm + crudsql + gin on MySQL, %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(r.Run(*addr))
}

// bootstrap migrates the table and reseeds it, so the example runs the same
// way every time it starts. A real application would use its own migrations.
func bootstrap(db *gorm.DB) error {
	if err := db.AutoMigrate(&Product{}); err != nil {
		return err
	}
	if err := db.Exec("DELETE FROM gorm_mysql_products").Error; err != nil {
		return err
	}
	rows := []Product{
		{Sku: "BOLT-1", Name: "hex bolt", Price: 250, Stock: crud.Set(40), Active: true},
		{Sku: "NUT-1", Name: "hex nut", Price: 120, Active: true},
		{Sku: "WSH-1", Name: "washer", Price: 35, Stock: crud.Set(900), Active: true},
	}
	return db.Create(&rows).Error
}
