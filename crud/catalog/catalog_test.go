package catalog

import (
	"context"
	"reflect"
	"testing"
)

func TestAConstraintIsKeyedOnItsTableAsWellAsItsName(t *testing.T) {
	ctx := context.Background()
	s := pgSchema{
		columns: [][]any{
			pgColumnRow("users", "id", 1),
			pgColumnRow("orders", "ref", 1),
		},
		constraints: [][]any{
			pgConstraintRow("users", "PRIMARY", "p", 1, "id"),
			pgConstraintRow("orders", "PRIMARY", "p", 1, "ref"),
		},
	}
	cat, err := Load(ctx, recorder(s, 1))
	if err != nil {
		t.Fatal(err)
	}

	users, ok := cat.Constraint("users", "PRIMARY")
	if !ok {
		t.Fatal("users has no PRIMARY")
	}
	orders, ok := cat.Constraint("orders", "PRIMARY")
	if !ok {
		t.Fatal("orders has no PRIMARY")
	}

	if users.Table == orders.Table {
		t.Errorf("both PRIMARYs belong to %q — the name was looked up without its table", users.Table)
	}
	if reflect.DeepEqual(users.Columns, orders.Columns) {
		t.Errorf("both PRIMARYs cover %v, so a probe would bind the wrong table's key", users.Columns)
	}
}

func TestColumnsAndConstraintsKeepTheOrderTheEngineReported(t *testing.T) {
	ctx := context.Background()
	engineColumns := []string{"id", "zeta", "alpha", "note", "beta", "yankee", "code", "aardvark"}
	engineConstraints := []string{"zz_pkey", "alpha_key", "mid_key", "aaa_key", "yy_key", "bb_key", "cc_key", "nn_key"}

	s := pgSchema{}
	for i, name := range engineColumns {
		s.columns = append(s.columns, pgColumnRow("rows", name, i+1))
	}
	for i, name := range engineConstraints {
		kind := "u"
		if i == 0 {
			kind = "p"
		}
		s.constraints = append(s.constraints, pgConstraintRow("rows", name, kind, 1, engineColumns[i]))
	}

	var first []string
	for run := range 2 {
		cat, err := Load(ctx, recorder(s, 1))
		if err != nil {
			t.Fatal(err)
		}
		tbl, ok := cat.Table("rows")
		if !ok {
			t.Fatal("no table")
		}

		var cols, cons []string
		for _, c := range tbl.Columns {
			cols = append(cols, c.Name)
		}
		for _, c := range tbl.Constraints {
			cons = append(cons, c.Name)
		}
		if !reflect.DeepEqual(cols, engineColumns) {
			t.Fatalf("columns came back %v, and the engine reported %v", cols, engineColumns)
		}
		if !reflect.DeepEqual(cons, engineConstraints) {
			t.Fatalf("constraints came back %v, and the engine reported %v", cons, engineConstraints)
		}

		got := append(append([]string{}, cols...), cons...)
		if run == 0 {
			first = got
			continue
		}
		if !reflect.DeepEqual(first, got) {
			t.Errorf("two loads of one schema disagreed:\n  %v\n  %v", first, got)
		}
	}
}

func TestANilTableAnswersFalseRatherThanPanicking(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("a nil table panicked: %v", p)
		}
	}()
	var missing *Table
	if _, ok := missing.Column("id"); ok {
		t.Error("a nil table found a column")
	}
	if _, ok := missing.Constraint("pk"); ok {
		t.Error("a nil table found a constraint")
	}

	cat, err := Load(context.Background(), recorder(oneTable(), 1))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := cat.Table("rows")
	if !ok {
		t.Fatal("no table")
	}
	if _, ok := tbl.Column("id"); !ok {
		t.Error("a real table could not find its own column")
	}
	if _, ok := tbl.Constraint("rows_pkey"); !ok {
		t.Error("a real table could not find its own constraint")
	}
}

func TestEveryKindPrintsItsOwnName(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range []Kind{KindPrimaryKey, KindUnique, KindUniqueIndex, KindForeignKey, KindCheck} {
		s := k.String()
		if s == "unknown" {
			t.Errorf("kind %d prints as unknown", k)
		}
		if seen[s] {
			t.Errorf("two kinds print as %q, so a failure message cannot tell them apart", s)
		}
		seen[s] = true
	}
	if Kind(0).String() != "unknown" {
		t.Error("the zero kind claims to be something")
	}
}
