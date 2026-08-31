package sqlerr_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/frostgrove/vv/errs/sqlerr"
)

func TestSameKeySeparatesTwoCapturesThatWouldClassifyDifferently(t *testing.T) {
	all := corpora(t)

	uq := find(t, all["postgres"], "unique").Err
	if !uq.SameKey(uq) {
		t.Fatal("a capture does not read as its own key, so every refusal below is a comparator that answers no to everything")
	}

	plain := find(t, all["mysql"], "unique").Err
	twin := find(t, all["mysql"], "unique_in_another_locale").Err
	if plain.Message == twin.Message {
		t.Fatalf("the two mysql captures now carry one sentence, so their agreeing on it shows nothing:\n  %s", plain.Message)
	}
	if !plain.SameKey(twin) {
		t.Errorf("one violation captured in two languages reads as two keys, so the text is part of the comparison\n  en: %s\n  ru: %s",
			plain.Message, twin.Message)
	}

	for _, tc := range []struct {
		moved string
		a, b  *sqlerr.Err
		held  func(a, b *sqlerr.Err) bool
		what  string
	}{
		{
			moved: "the SQLSTATE",
			a:     find(t, all["mysql"], "bad_type").Err,
			b:     find(t, all["mariadb"], "bad_type").Err,
			held:  func(a, b *sqlerr.Err) bool { return a.Type == b.Type && a.Native == b.Native },
			what:  "one driver type and one number",
		},
		{
			moved: "the native number",
			a:     find(t, all["mysql"], "unique").Err,
			b:     find(t, all["mysql"], "foreign_key").Err,
			held:  func(a, b *sqlerr.Err) bool { return a.Type == b.Type && a.SQLState == b.SQLState },
			what:  "one driver type and one SQLSTATE",
		},
		{
			moved: "the driver type",
			a:     find(t, all["mysql"], "connect_failure").Err,
			b:     find(t, all["postgres"], "connect_failure").Err,
			held:  func(a, b *sqlerr.Err) bool { return a.SQLState == b.SQLState && a.Native == b.Native },
			what:  "no SQLSTATE and no number on either side",
		},
	} {
		if !tc.held(tc.a, tc.b) {
			t.Fatalf("the pair for %s no longer shares %s, so refusing it says nothing about %s\n  %s\n  %s",
				tc.moved, tc.what, tc.moved, tc.a.Key(), tc.b.Key())
		}
		if tc.a.SameKey(tc.b) {
			t.Errorf("%s moved and the two still read as one key, so %s is no longer compared\n  %s\n  %s",
				tc.moved, tc.moved, tc.a.Key(), tc.b.Key())
		}
	}

	pg := find(t, all["postgres"], "unique").Err
	if _, ok := pg.Fields["ConstraintName"]; !ok {
		t.Fatalf("postgres/unique no longer records a ConstraintName, so removing one removes nothing: %s", pg.Key())
	}
	if whole := copyErr(pg); !pg.SameKey(whole) {
		t.Fatalf("a field-for-field copy already reads as another key, so the removal below is not what any refusal is about\n  %s\n  %s",
			pg.Key(), whole.Key())
	}
	fewer := copyErr(pg)
	delete(fewer.Fields, "ConstraintName")
	if pg.SameKey(fewer) {
		t.Errorf("a capture naming the constraint and one not naming it read as one key, so a Source that lost its constraint name would not be a finding\n  %s\n  %s",
			pg.Key(), fewer.Key())
	}

	none := find(t, all["sqlite"], "too_long").Err
	other := find(t, all["mysql"], "transaction_aborted").Err
	if none != nil || other != nil {
		t.Fatalf("both unreachable cases now carry an error, so the two legs below compare something else")
	}
	if !none.SameKey(other) {
		t.Error("two absent captures read as different keys, so every engine that cannot reach a case would be a finding on every run")
	}
	if none.SameKey(uq) {
		t.Error("an absent capture reads as the same key as a real one, so a case that stopped being provoked at all would go unnoticed")
	}
}

func copyErr(e *sqlerr.Err) *sqlerr.Err {
	c := *e
	c.Fields = make(map[string]string, len(e.Fields))
	for k, v := range e.Fields {
		c.Fields[k] = v
	}
	return &c
}

func TestSavingAnUnchangedCorpusRewritesNothing(t *testing.T) {
	tmp := t.TempDir()
	for _, engine := range engines {
		c, err := sqlerr.Load(dir, engine)
		if err != nil {
			t.Fatalf("reading the checked-in corpus: %v", err)
		}
		want, err := os.ReadFile(sqlerr.Path(dir, engine))
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlerr.Save(tmp, c); err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		got, err := os.ReadFile(sqlerr.Path(tmp, engine))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: loading and saving the checked-in file changed it (%d bytes to %d), so every recapture rewrites it whether the server moved or not",
				engine, len(want), len(got))
		}

		c.Server += " (not this server)"
		if err := sqlerr.Save(tmp, c); err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		again, err := os.ReadFile(sqlerr.Path(tmp, engine))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(again, want) {
			t.Errorf("%s: a corpus naming a different server saved to the same bytes, so the comparison above is not reading what Save wrote", engine)
		}
	}
}
