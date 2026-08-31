package main

import (
	"flag"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/utils/vvdb"
)

//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly CreatedAt

type Product struct {
	ID        int64         `gorm:"primaryKey" db:"id,pk,auto" json:"id"`
	Sku       string        `gorm:"size:64;uniqueIndex" db:"sku" json:"sku"`
	Name      string        `gorm:"size:200" db:"name" json:"name"`
	Price     int           `db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `db:"active" json:"active"`
	CreatedAt time.Time     `gorm:"autoCreateTime;default:CURRENT_TIMESTAMP(3)" db:"created_at,generated" json:"createdAt"`
}

func (Product) TableName() string { return "gorm_mysql_products" }

var Products = sqlrepo.Define[Product, int64, ProductUpdate]("gorm_mysql_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

var database = vvdb.Config{
	Engine: vvdb.MySQL, Host: "localhost", Port: 53306,
	User: "vv", Password: "vv", Name: "vv", SSLMode: "disable",
	Params: map[string]string{"loc": "UTC"},
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	dsn, err := vvdb.MySQLDSN(&database)
	if err != nil {
		log.Fatal(err)
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
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

	repository := specs.Executor(Products.Bind(crudsql.MySQL(sqlDB)))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	crudgin.New(repository,
		crudgin.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Mount(r, "/products")

	log.Printf("gorm + crudsql + gin on MySQL, %s — try /products?f=price:gte:100&sort=-price", *addr)
	log.Fatal(r.Run(*addr))
}

func bootstrap(database *gorm.DB) error {
	if err := database.AutoMigrate(&Product{}); err != nil {
		return err
	}
	if err := database.Exec("DELETE FROM gorm_mysql_products").Error; err != nil {
		return err
	}
	rows := []Product{
		{Sku: "BOLT-1", Name: "hex bolt", Price: 250, Stock: crud.Set(40), Active: true},
		{Sku: "NUT-1", Name: "hex nut", Price: 120, Active: true},
		{Sku: "WSH-1", Name: "washer", Price: 35, Stock: crud.Set(900), Active: true},
	}
	return database.Create(&rows).Error
}
