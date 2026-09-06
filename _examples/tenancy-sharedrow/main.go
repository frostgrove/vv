// A composition root that turns one verified tenant into a narrowed statement, an
// object namespace and a bounded cross-tenant run — with no tenant argument
// anywhere in the business code below. The cache and durable-job seams are wired
// the same way, from `tenancycache` and `tenancyjobs`, and need a backend and a
// queue this example deliberately does not start.
//
// It builds and vets without a database on purpose: what it demonstrates is the
// wiring, and the wiring is the part a consumer copies.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
	"github.com/frostgrove/vv/tenancy/tenancystorage"
)

type Invoice struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	Number   string `db:"number"`
	Total    int64  `db:"total"`
	Lines    []Line `rel:"has_many,fk=InvoiceID"`
}

type InvoiceUpdate struct {
	Number *string
	Total  *int64
}

type Line struct {
	ID        int64  `db:"id,pk,auto"`
	TenantID  int64  `db:"tenant_id"`
	InvoiceID int64  `db:"invoice_id"`
	Text      string `db:"text"`
}

var Invoices = sqlrepo.Define[Invoice, int64, InvoiceUpdate]("invoices")

// The control plane. In a real deployment this reaches a directory, a database or
// an API; what the extension needs from it is three plain values, and never a
// tenancy.Scope — if application code could return the accepted type, the type
// would be constructible by application code.
type controlPlane struct{ tenants map[string]tenancy.Resolution }

func (this controlPlane) Resolve(ctx context.Context) (tenancy.Resolution, error) {
	requested, ok := ctx.Value(requestedTenant{}).(string)
	if !ok {
		return tenancy.Resolution{}, tenancy.ErrNoScope
	}
	return this.lookup(requested)
}

func (this controlPlane) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	return this.lookup(reference.Value())
}

func (this controlPlane) lookup(raw string) (tenancy.Resolution, error) {
	resolution, ok := this.tenants[raw]
	if !ok {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return resolution, nil
}

type requestedTenant struct{}

func main() {
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:   directory(),
		Origin:     "invoices.eu-west-1",
		DurableKey: durableKey(),
	})
	if err != nil {
		panic(err)
	}

	// One line at the composition root. Nothing below it mentions a tenant.
	ownership := tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, numericTenant,
		tenancyrow.Relation{Path: "Lines", Field: "TenantID"})
	middleware := tenancyrow.Repository[Invoice, int64](authority, ownership)

	fmt.Printf("invoices are narrowed by %T\n", middleware)
	fmt.Println("a request with no verified tenant:", refusalFor(authority, context.Background()))

	bound, err := authority.Bind(context.WithValue(context.Background(), requestedTenant{}, "42"), tenancy.ClassWrite)
	if err != nil {
		panic(err)
	}
	fmt.Println("a request for an active tenant:", refusalFor(authority, bound))

	namespace, err := tenancystorage.Namespace(bound, authority, "invoices", tenancy.ClassWrite)
	if err != nil {
		panic(err)
	}
	fmt.Println("its object namespace is a fixed-width digest:", namespace.Value())

	grant, err := authority.Accept(billing(), cohort(), time.Now().Add(time.Hour), tenancy.ClassRead)
	if err != nil {
		panic(err)
	}
	fmt.Printf("a cohort grant covers %d tenants, for reads only\n", grant.Size())
}

// A literal here would be long enough to be accepted, and every deployment that
// copied this wiring would authenticate its queue records with a key printed in
// the repository — which is queue write access turned into tenant impersonation.
// The same value has to reach the producer and the worker, so it comes from the
// secret store; without one this example runs on a key it throws away, which is
// correct for a process that enqueues nothing.
func durableKey() []byte {
	if configured := os.Getenv("TENANCY_DURABLE_KEY"); len(configured) >= tenancy.MinDurableKeyBytes {
		return []byte(configured)
	}
	key := make([]byte, tenancy.MinDurableKeyBytes)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	fmt.Println("no TENANCY_DURABLE_KEY in the environment, so this run sealed nothing another process can read")
	return key
}

func directory() tenancy.Resolver {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		panic(err)
	}
	tenants := map[string]tenancy.Resolution{}
	for _, raw := range []string{"42", "43"} {
		reference, err := tenancy.ParseReference(raw)
		if err != nil {
			panic(err)
		}
		tenants[raw] = tenancy.Resolution{Reference: reference, Lifecycle: tenancy.Active, Epoch: epoch}
	}
	return controlPlane{tenants: tenants}
}

// The ownership column is a BIGINT and the reference is opaque, so the mapping
// between them is the deployment's — the extension never guesses it.
func numericTenant(reference tenancy.Reference) (any, error) {
	value, err := strconv.ParseInt(reference.Value(), 10, 64)
	if err != nil {
		return nil, tenancy.ErrMalformed
	}
	return value, nil
}

func refusalFor(authority *tenancy.Authority, ctx context.Context) string {
	scope, err := authority.Scope(ctx, tenancy.ClassWrite)
	switch {
	case errors.Is(err, tenancy.ErrNoScope):
		return "refused before any statement, and the message names no tenant"
	case err != nil:
		return err.Error()
	default:
		// Not the reference: it is redacted everywhere else in this extension, and
		// an example that prints it here is the one a consumer copies into a log.
		return "bound to a tenant this deployment lists as " + scope.Lifecycle().String()
	}
}

func billing() tenancy.Purpose {
	purpose, err := tenancy.ParsePurpose("monthly-billing")
	if err != nil {
		panic(err)
	}
	return purpose
}

func cohort() []tenancy.Reference {
	var references []tenancy.Reference
	for _, raw := range []string{"42", "43"} {
		reference, err := tenancy.ParseReference(raw)
		if err != nil {
			panic(err)
		}
		references = append(references, reference)
	}
	return references
}
