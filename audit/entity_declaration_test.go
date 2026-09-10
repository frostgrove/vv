package audit_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
)

type declarationOrder struct {
	ID        int64      `db:"id,pk"`
	Status    string     `db:"status"`
	Approved  *time.Time `db:"approved_at"`
	Secret    string     `db:"secret,secret"`
	DeletedAt *time.Time `db:"deleted_at,tombstone"`
}

func declarationPolicySemantics() audit.PolicySemantics {
	return audit.Semantics(1, audit.PolicyGolden("fixture.one", strings.Repeat("01", 32)))
}

func declarationDescriptor(resource audit.Resource, action audit.Action) audit.Descriptor {
	return audit.Descriptor{
		Resource: resource, Action: action, Owner: "commerce", Purpose: "accountability",
		Retention: "business.long", Consequence: audit.Required,
	}
}

func declarationMeta(t *testing.T, table string) *crud.Meta {
	t.Helper()
	meta, err := crud.NewMeta[declarationOrder](table)
	if err != nil {
		t.Fatalf("construct declaration metadata: %v", err)
	}
	return meta
}

func TestResourceDeclarationBindsExactMetadataAndCapturesUsableDrafts(t *testing.T) {
	meta := declarationMeta(t, "declaration_orders")
	selectorCalls := 0
	statusMember := audit.Member(func(model *declarationOrder) *string {
		selectorCalls++
		return &model.Status
	})
	status := audit.ReconstructBy(statusMember, "status", audit.Text(), audit.Internal)
	approved := audit.Optional[declarationOrder]("Approved", "approved_at", audit.Time(), audit.Internal)
	secret := audit.Tokenized[declarationOrder]("Secret", "secret_token", audit.Text(), audit.Secret)
	actions := audit.Actions(audit.EntityRestored, audit.EntityChanged, audit.EntityCreated, audit.EntitySoftDeleted)
	fields := audit.Fields[declarationOrder](secret, approved, status)
	policy, err := audit.TryDefine(audit.Policy[declarationOrder, int64]{
		Model: meta, Semantics: declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order", ""),
		Subject:    audit.PlaintextSubject(func(id int64) string { return "order-" + string(rune('a'+id)) }, audit.Internal),
		Actions:    actions, Fields: fields,
	})
	if err != nil {
		t.Fatalf("define resource declaration: %v", err)
	}
	if selectorCalls != 1 {
		t.Fatalf("member selector ran %d times during declaration, want exactly once", selectorCalls)
	}
	actions[0] = audit.EntityHardDeleted
	fields[0] = nil
	description := policy.Description()
	if len(description.Actions) != 4 || description.Actions[0] != audit.EntityChanged {
		t.Fatalf("canonical copied actions = %v", description.Actions)
	}
	if len(description.Fields) != 3 || description.Fields[0].Name != "approved_at" || description.Fields[1].Name != "secret_token" || description.Fields[2].Name != "status" {
		t.Fatalf("canonical copied fields = %+v", description.Fields)
	}
	description.Fields[0].Codec.ReadVersions[0] = 99
	description.Actions[0] = audit.EntityHardDeleted
	again := policy.Description()
	if again.Fields[0].Codec.ReadVersions[0] != 1 || again.Actions[0] != audit.EntityChanged {
		t.Fatal("mutating a declaration description changed the sealed policy")
	}
	before := &declarationOrder{ID: 1, Status: "pending", Secret: "old"}
	after := &declarationOrder{ID: 1, Status: "approved", Secret: "new"}
	if _, err := policy.Created(before); err != nil {
		t.Fatalf("capture create draft: %v", err)
	}
	if _, changed, err := policy.Changed(before, after); err != nil || !changed {
		t.Fatalf("capture changed draft: changed=%v err=%v", changed, err)
	}
	if _, changed, err := policy.Changed(after, after); err != nil || changed {
		t.Fatalf("equal models produced a change: changed=%v err=%v", changed, err)
	}
	if _, err := policy.Deleted(after); err != nil {
		t.Fatalf("capture soft-delete draft: %v", err)
	}
	if _, err := policy.Restored(after); err != nil {
		t.Fatalf("capture restore draft: %v", err)
	}
	if selectorCalls != 1 {
		t.Fatalf("hot-path capture reran the member selector: %d calls", selectorCalls)
	}
}

func TestResourceDeclarationRejectsWrongMetadataSecretsAndSelectors(t *testing.T) {
	meta := declarationMeta(t, "declaration_orders_invalid")
	base := audit.Policy[declarationOrder, int64]{
		Model: meta, Semantics: declarationPolicySemantics(),
		Descriptor: declarationDescriptor("commerce.order.invalid", ""),
		Subject:    audit.PlaintextSubject(func(id int64) string { return "order" }, audit.Internal),
		Actions:    audit.Actions(audit.EntityCreated),
	}
	if _, err := audit.TryDefine(base); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("empty field policy error = %v, want ErrDeclaration", err)
	}
	valid := base
	valid.Fields = audit.Fields[declarationOrder](
		audit.Value[declarationOrder]("Status", "status", audit.Text(), audit.Internal),
	)
	nilFieldMeta := *meta
	nilFieldSchema := *meta.Schema
	nilFieldSchema.Fields = append([]*crud.Field(nil), meta.Fields...)
	nilFieldSchema.Fields[1] = nil
	nilFieldMeta.Schema = &nilFieldSchema
	nilFieldPolicy := valid
	nilFieldPolicy.Model = &nilFieldMeta
	if _, err := audit.TryDefine(nilFieldPolicy); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("nil metadata field error = %v, want ErrDeclaration", err)
	}
	foreignPKMeta := *meta
	foreignPKSchema := *meta.Schema
	foreignPK := *meta.PK
	foreignPK.Offset = meta.Fields[1].Offset
	foreignPKSchema.PK = &foreignPK
	foreignPKMeta.Schema = &foreignPKSchema
	foreignPKPolicy := valid
	foreignPKPolicy.Model = &foreignPKMeta
	if _, err := audit.TryDefine(foreignPKPolicy); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("foreign metadata primary key error = %v, want ErrDeclaration", err)
	}
	wrongID := audit.Policy[declarationOrder, string]{
		Model: meta, Semantics: base.Semantics, Descriptor: base.Descriptor,
		Subject: audit.PlaintextSubject(func(string) string { return "order" }, audit.Internal), Actions: base.Actions,
	}
	if _, err := audit.TryDefine(wrongID); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("wrong primary-key type error = %v, want ErrDeclaration", err)
	}
	secretProtected := base
	secretProtected.Fields = audit.Fields[declarationOrder](
		audit.Protected[declarationOrder]("Secret", "secret", audit.Text(), audit.Secret),
	)
	if _, err := audit.TryDefine(secretProtected); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("reversible secret field error = %v, want ErrDeclaration", err)
	}
	external := "outside"
	badMember := audit.Member(func(*declarationOrder) *string { return &external })
	badSelector := base
	badSelector.Fields = audit.Fields[declarationOrder](
		audit.ValueBy(badMember, "external", audit.Text(), audit.Internal),
	)
	if _, err := audit.TryDefine(badSelector); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("external member selector error = %v, want ErrDeclaration", err)
	}
	unknown := base
	unknown.Fields = audit.Fields[declarationOrder](
		audit.Value[declarationOrder]("Missing", "missing", audit.Text(), audit.Internal),
	)
	if _, err := audit.TryDefine(unknown); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("unknown metadata field error = %v, want ErrDeclaration", err)
	}
}

func TestResourceDeclarationShortFormPanicsOnInvalidPolicy(t *testing.T) {
	deferred := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		audit.Define(audit.Policy[declarationOrder, int64]{})
		return false
	}()
	if !deferred {
		t.Fatal("Define accepted an empty policy without panicking")
	}
}
