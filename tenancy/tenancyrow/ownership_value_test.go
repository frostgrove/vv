package tenancyrow_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
)

type StringOwned struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID string `db:"tenant_id"`
	Title    string `db:"title"`
}

type Tagged struct {
	ID    int64      `db:"id,pk,auto"`
	Title string     `db:"title"`
	Tags  []OwnedTag `rel:"has_many,fk=TaggedID,ref=ID"`
}

type OwnedTag struct {
	ID       int64  `db:"id,pk,auto"`
	TaggedID int64  `db:"tagged_id"`
	TenantID string `db:"tenant_id"`
}

func answering(value any) tenancyrow.Value {
	return func(tenancy.Reference) (any, error) { return value, nil }
}

// Go converts an int to a string by reading it as a rune and a wide integer to a
// narrow one by truncating, so a strategy that reached for ConvertibleTo would
// narrow on a tenant nobody has and stamp a row with it. Tenant 65 became "A",
// and any tenant whose reference is "A" then owned that row.
func TestAnOwnershipValueTheColumnCannotHoldIsRefusedRatherThanConverted(t *testing.T) {
	for name, value := range map[string]any{
		"an int against a string column":    65,
		"a wider int than the column holds": int64(300),
		"a struct the column cannot hold":   struct{ A int }{1},
	} {
		t.Run(name, func(t *testing.T) {
			ownership := tenancyrow.Column[StringOwned]("TenantID", tenancyrow.Derive, answering(value))

			if _, err := ownership.Narrow(reference(t, secretReference)); err == nil {
				t.Fatal("the read path accepted an ownership value the column cannot hold, so it narrowed on a tenant that does not exist")
			}
			if err := ownership.Apply(reference(t, secretReference), crud.ActionCreate, &StringOwned{}); err == nil {
				t.Fatal("the write path accepted it, so the row was stamped with a tenant that does not exist")
			}
		})
	}
}

// crud.Eq turns a missing value into IS NULL, which matches every row nobody
// owns rather than none — the rows a shared-row schema keeps for templates,
// defaults and pre-migration data. Every spelling of "no value" has to refuse.
func TestAnAbsentOwnershipValueIsRefusedRatherThanNarrowingToNull(t *testing.T) {
	for name, value := range map[string]any{
		"an untyped nil": nil,
		"a nil pointer":  (*string)(nil),
		"a null Opt":     crud.Null[string](),
	} {
		t.Run(name, func(t *testing.T) {
			ownership := tenancyrow.Column[StringOwned]("TenantID", tenancyrow.Derive, answering(value))

			predicate, err := ownership.Narrow(reference(t, secretReference))
			if err == nil {
				t.Fatalf("an absent ownership value produced a predicate rather than a refusal: %v", predicate)
			}
			if err := ownership.Apply(reference(t, secretReference), crud.ActionCreate, &StringOwned{}); err == nil {
				t.Fatal("an absent ownership value was accepted as ownership on create")
			}
		})
	}
}

// The control: the shapes a deployment actually uses must keep working, or the
// two tests above would pass with the strategy refusing everything.
func TestAnOwnershipValueTheColumnHoldsStillNarrowsAndStamps(t *testing.T) {
	ownership := tenancyrow.Column[StringOwned]("TenantID", tenancyrow.Derive, nil)

	predicate, err := ownership.Narrow(reference(t, secretReference))
	if err != nil {
		t.Fatalf("a reference that matches the column type was refused: %v", err)
	}
	if predicate == nil {
		t.Fatal("a reference the column can hold produced no narrowing at all")
	}

	row := &StringOwned{}
	if err := ownership.Apply(reference(t, secretReference), crud.ActionCreate, row); err != nil {
		t.Fatalf("a create was refused for its own tenant: %v", err)
	}
	if row.TenantID != secretReference {
		t.Fatalf("the create was not stamped with the bound tenant: %q", row.TenantID)
	}
}

// Ownership through a to-many path is not ownership. It compiles to "at least
// one row on the far side is mine", so a parent whose children belong to two
// tenants answers to both of them, for reads and for writes — and the key it
// would freeze is the parent's own identity rather than a link to an owner.
func TestOwnershipThroughAToManyRelationIsRefusedAtWiring(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("a to-many path was accepted as ownership, so every tenant on the far side shares the row")
		}
		if !strings.Contains(strings.ToLower(strings.TrimSpace(toString(recovered))), "to-many") {
			t.Fatalf("the refusal does not say why: %v", recovered)
		}
	}()
	_ = tenancyrow.Through[Tagged]("Tags", "TenantID", nil)
}

func toString(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// The consumer's Value callback reaches whatever the deployment's directory is,
// and its error names the tenant, the database and often the credential. The
// authority already collapses a resolver's text for exactly that reason; this
// seam calls application code the same way and must answer the same way.
func TestAValueCallbackFailureNeverTravelsBackAsText(t *testing.T) {
	leaking := errors.New("tenant directory: no shard for acme-7f3c (db=pg-eu-3, user=svc_acme)")
	ownership := tenancyrow.Column[StringOwned]("TenantID", tenancyrow.Derive,
		func(tenancy.Reference) (any, error) { return nil, leaking })

	_, err := ownership.Narrow(reference(t, secretReference))
	if err == nil {
		t.Fatal("a failing ownership callback produced a predicate")
	}
	if strings.Contains(err.Error(), "pg-eu-3") || strings.Contains(err.Error(), "svc_acme") ||
		strings.Contains(err.Error(), secretReference) {
		t.Fatalf("the callback's own text travelled back to the caller: %v", err)
	}
	if !errors.Is(err, tenancy.ErrUnavailable) {
		t.Fatalf("the refusal is not one of this package's: %v", err)
	}
}

// A deliberate sentinel is the one thing a callback may say, and it has to keep
// its kind: a malformed tenant is a bad request, not an internal error.
func TestAValueCallbackKeepsTheSentinelItChose(t *testing.T) {
	ownership := tenancyrow.Column[StringOwned]("TenantID", tenancyrow.Derive,
		func(tenancy.Reference) (any, error) { return nil, tenancy.ErrMalformed })

	_, err := ownership.Narrow(reference(t, secretReference))
	if !errors.Is(err, tenancy.ErrMalformed) {
		t.Fatalf("the sentinel the callback chose was collapsed: %v", err)
	}
	if !errors.Is(err, crud.ErrBadRequest) {
		t.Fatalf("a malformed tenant reads as an internal error rather than a bad request: %v", err)
	}
}
