package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
)

type subjectModel struct {
	ID   int64  `db:"id,pk"`
	Name string `db:"name"`
}

func subjectPolicy(t *testing.T, table string, mapper func(int64) string) *audit.ResourcePolicy[subjectModel, int64] {
	t.Helper()
	meta, err := crud.NewMeta[subjectModel](table)
	if err != nil {
		t.Fatal(err)
	}
	return audit.Define(audit.Policy[subjectModel, int64]{
		Model: meta,
		Semantics: audit.Semantics(1,
			audit.PolicyGolden("subject.fixture", strings.Repeat("09", 32))),
		Descriptor: audit.Descriptor{
			Resource: "subject.model", Owner: "subject.team", Purpose: "accountability",
			Retention: "subject.forever", Consequence: audit.Required,
		},
		Subject: audit.TokenizedSubject(mapper, audit.Internal),
		Actions: audit.Actions(audit.EntityCreated, audit.EntityChanged, audit.EntityHardDeleted),
		Fields: audit.Fields[subjectModel](
			audit.Value[subjectModel]("Name", "name", audit.Text(), audit.Internal),
		),
	})
}

func TestSubjectRefBindsTheExactResourceAndDeclaredPrivacy(t *testing.T) {
	policy := subjectPolicy(t, "subject_models", func(id int64) string { return "subject-42" })
	reference, err := policy.SubjectRef(42)
	if err != nil {
		t.Fatal(err)
	}
	view := reference.View()
	if view.Resource != "subject.model" || view.Subject != "subject-42" || view.Mode != audit.AsToken || view.Classification != audit.Internal {
		t.Fatalf("subject view = %+v", view)
	}
}

func TestSubjectRefConvertsMapperPanicsAndInvalidReferences(t *testing.T) {
	panics := subjectPolicy(t, "subject_models", func(int64) string { panic("secret mapper detail") })
	if _, err := panics.SubjectRef(42); !errors.Is(err, audit.ErrInvalid) || strings.Contains(err.Error(), "secret mapper detail") {
		t.Fatalf("panic error = %v", err)
	}
	invalid := subjectPolicy(t, "subject_models", func(int64) string { return "" })
	if _, err := invalid.SubjectRef(42); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("invalid reference error = %v", err)
	}
}

func TestResourcePolicyChecksTheExactRepositoryMetadata(t *testing.T) {
	policy := subjectPolicy(t, "subject_models", func(int64) string { return "subject-42" })
	exact, err := crud.NewMeta[subjectModel]("subject_models")
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.CheckModel(exact); err != nil {
		t.Fatalf("exact metadata: %v", err)
	}
	wrong, err := crud.NewMeta[subjectModel]("other_subject_models")
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.CheckModel(wrong); !errors.Is(err, audit.ErrWrongCatalog) {
		t.Fatalf("wrong metadata error = %v", err)
	}
}
