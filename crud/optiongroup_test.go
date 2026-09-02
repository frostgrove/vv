package crud_test

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestAVerbRefusesTheOptionItCannotHonour(t *testing.T) {
	for _, tc := range []struct {
		name   string
		group  crud.OptionGroup
		option crud.Option
		blame  string
	}{
		{"a write cannot page", crud.MutationOptions, crud.Limit(5), "Limit"},
		{"a write cannot sort", crud.MutationOptions, crud.OrderBy(crud.Asc("ID")), "OrderBy"},
		{"a write cannot project", crud.MutationOptions, crud.Select("Name"), "Select"},
		{"a write cannot preload", crud.MutationOptions, crud.Preload("Owner"), "Preload"},
		{"an aggregate has no cursor", crud.AggregateOptions, crud.After("token"), "After"},
		{"an aggregate has nothing to preload onto", crud.AggregateOptions, crud.Preload("Owner"), "Preload"},
		{"an aggregate projects its own columns", crud.AggregateOptions, crud.Select("Name"), "Select"},
		{"a read answers with rows", crud.ReadOptions, crud.Aggregate(crud.CountAll("n")), "Aggregate"},
		{"a preload cannot be paginated", crud.PreloadOptions, crud.Limit(5), "Limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := tc.group.Build("Model", tc.option)
			if err == nil {
				t.Fatalf("the option was accepted and would be silently dropped: %+v", o)
			}
			var schema *crud.SchemaError
			if !errors.As(err, &schema) {
				t.Fatalf("err = %T (%v), want the schema error a transport already turns into a 400", err, err)
			}
			if schema.Field != tc.blame {
				t.Fatalf("the refusal blames %q; the caller wrote crud.%s", schema.Field, tc.blame)
			}
			if schema.Model != "Model" {
				t.Fatalf("the refusal names %q rather than the model it was asked about", schema.Model)
			}
		})
	}
}

func TestCheckAndBuildGiveTheSameAnswerOverAnOptionsValue(t *testing.T) {
	options := crud.Build(crud.Where(crud.Eq("ID", 1)), crud.Limit(5))

	viaCheck := crud.MutationOptions.Check("Model", options)
	_, viaBuild := crud.MutationOptions.Build("Model", crud.Where(crud.Eq("ID", 1)), crud.Limit(5))

	if viaCheck == nil || viaBuild == nil {
		t.Fatalf("Check = %v, Build = %v; both must refuse crud.Limit on a write", viaCheck, viaBuild)
	}
	if viaCheck.Error() != viaBuild.Error() {
		t.Fatalf("the explicit check says %q and the resolving one says %q", viaCheck, viaBuild)
	}
	if err := crud.MutationOptions.Check("Model", crud.Build(crud.Where(crud.Eq("ID", 1)))); err != nil {
		t.Fatalf("a narrowed write was refused by the explicit check: %v", err)
	}
}

func TestAVerbAcceptsWhatItActuallyHonours(t *testing.T) {
	for _, tc := range []struct {
		name    string
		group   crud.OptionGroup
		options []crud.Option
	}{
		{"a write narrows, locks and names its datasource", crud.MutationOptions,
			[]crud.Option{crud.Where(crud.Eq("ID", 1)), crud.ForUpdate(), crud.PrimaryOnly()}},
		{"an aggregate groups, sorts and pages", crud.AggregateOptions,
			[]crud.Option{crud.Aggregate(crud.CountAll("n")), crud.GroupBy("Tenant"),
				crud.OrderBy(crud.Asc("Tenant")), crud.Limit(10), crud.PrimaryOnly()}},
		{"a read shapes its whole response", crud.ReadOptions,
			[]crud.Option{crud.Select("Name"), crud.Preload("Owner"), crud.Limit(10),
				crud.After("token"), crud.Distinct(), crud.PrimaryOnly(), crud.ForUpdate()}},
		{"a preload filters, sorts and caps", crud.PreloadOptions,
			[]crud.Option{crud.Where(crud.Eq("Spam", false)), crud.OrderBy(crud.Asc("ID")), crud.PreloadRows(5)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.group.Build("Model", tc.options...); err != nil {
				t.Fatalf("an option the verb honours was refused: %v", err)
			}
		})
	}
}
