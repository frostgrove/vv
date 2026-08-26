// Command pgx-grpc is vv on a transport that is not HTTP: a pgx v5 pool, the
// crudpgx adapter, and the gRPC binding.
//
// It is the pgx-fiber example with one line changed — the mount — which is the
// claim the port layer exists to make. The repository, the model, the query
// bounds and the rules are the same values.
//
//	go get github.com/frostgrove/vv
//	go get github.com/frostgrove/vv/crud/adapter/crudpgx
//	go get github.com/frostgrove/vv/crud/rpc/crudgrpc
//
// Run it with the repository's own databases up (`make up` at the root):
//
//	go run ./pgx-grpc
//
// There is no server reflection — a resource generic over its model has no
// compiled descriptor — so a client calls by full method name and sends a
// google.protobuf.Struct carrying the same JSON document the HTTP bindings
// speak. With grpcurl:
//
//	grpcurl -plaintext -d '{"limit":2,"sort":["-price"]}' \
//	  localhost:9090 vv.crud.v1.Product/List
//	grpcurl -plaintext -d '{"id":"1"}' localhost:9090 vv.crud.v1.Product/Get
//
// Note the key: `"1"` and not `1`. A protobuf number is a double, so a key
// travels as a string.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/rpc/crudgrpc"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly CreatedAt

// Product is the model: a plain struct, `db` tags, nothing generated and
// nothing embedded. `auto` says the database owns the key, so a create request
// cannot choose one; `generated` says the same about the timestamp.
type Product struct {
	ID        int64         `db:"id,pk,auto" json:"id"`
	Sku       string        `db:"sku" json:"sku"`
	Name      string        `db:"name" json:"name"`
	Price     int           `db:"price" json:"price"`
	Stock     crud.Opt[int] `db:"stock" json:"stock"`
	Active    bool          `db:"active" json:"active"`
	CreatedAt time.Time     `db:"created_at,generated" json:"createdAt"`
}

// Products is validated when this package initialises: a mistyped tag, a DTO
// field the model lacks or a wrong ID type fails here rather than at request
// time.
var Products = sqlrepo.Define[Product, int64, ProductUpdate]("pgx_grpc_products",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

const dsn = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := bootstrap(ctx, pool); err != nil {
		log.Fatal(err)
	}

	repo := specs.Executor(Products.Bind(crudpgx.Open(pool)))

	// The interceptor covers methods this binding did not write. The eight it
	// did render their own failures, so it is optional here and is installed
	// anyway, because that is what an application with hand-written methods
	// beside the CRUD ones does.
	srv := grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors()))
	crudgrpc.New(repo,
		crudgrpc.WithQuery[Product, int64, ProductUpdate](&query.Config{
			Filterable: []string{"Sku", "Name", "Price", "Stock", "Active", "CreatedAt"},
			Sortable:   []string{"Price", "Name", "CreatedAt"},
			Searchable: []string{"Sku", "Name"},
		}),
	).Register(srv, "Product")

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("pgx + crudpgx + grpc on %s — try vv.crud.v1.Product/List", *addr)
	log.Fatal(srv.Serve(lis))
}

// bootstrap creates the table and seeds it, so the example runs against an
// empty database. A real application would use its own migrations.
func bootstrap(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS pgx_grpc_products`,
		`CREATE TABLE pgx_grpc_products (
			id         BIGSERIAL PRIMARY KEY,
			sku        TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL,
			price      INT NOT NULL,
			stock      INT,
			active     BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO pgx_grpc_products (sku, name, price, stock) VALUES
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
