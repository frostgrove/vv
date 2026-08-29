package sqlerr_test

import (
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

// dir is where the checked-in corpus lives, relative to this package.
const dir = "testdata/corpus"

// engines is the four dialect strings Classify accepts, which are also the four
// file names. Written out rather than read off the directory: a file that
// stopped being loaded would otherwise shrink every table test below to three
// engines with nothing turning red.
var engines = []string{"postgres", "mysql", "mariadb", "sqlite"}

func corpora(t *testing.T) map[string]*sqlerr.Corpus {
	t.Helper()
	out := map[string]*sqlerr.Corpus{}
	for _, name := range engines {
		c, err := sqlerr.Load(dir, name)
		if err != nil {
			t.Fatalf("reading the checked-in corpus: %v", err)
		}
		out[name] = c
	}
	return out
}

func find(t *testing.T, c *sqlerr.Corpus, name string) sqlerr.Case {
	t.Helper()
	cs, ok := c.Case(name)
	if !ok {
		t.Fatalf("%s has no case named %q", c.Engine, name)
	}
	return cs
}

func isZero(s errs.Source) bool {
	return s.Table == "" && s.Schema == "" && s.Constraint == "" && s.Columns == nil
}

// The four words the corpus speaks that the public vocabulary does not. A
// parser must coarsen each to the code beside it, because a public code naming
// which index was hit is a hair away from naming the constraint ([[D-044]]).
var coarsened = map[string]errs.Code{
	"primary_key":     errs.CodeUnique,
	"not_null":        errs.CodeRequired,
	"missing_default": errs.CodeRequired,
	"bad_type":        errs.CodeInvalidFormat,
}

// Every case the corpus says is classifiable classifies, as the code its Want
// names — or, where Want is one of the four fine words, as the coarser code.
//
// The identity half is derived from StandardCodes rather than written out,
// which is what shrinks the hand-written table to the interesting rows: a
// parser can never answer a *different* declared code for a Want that names
// one, so writing those out would only be a second copy of the corpus.
func TestEveryClassifiableCorpusCaseGetsTheCodeItsClassNames(t *testing.T) {
	std := errs.StandardCodes()
	all := corpora(t)

	// The control, and it is the reason this test is not a tautology: the set
	// of Want values with no declared code must be exactly the four above.
	// Without it the loop below passes for a corpus with nothing classifiable
	// in it, for a fifth fine word silently left unclassified, and for a
	// coarsening row no engine exercises — and those four rows are what
	// errs/codes.go says this phase owes, so an unexercised one is an unpaid
	// debt wearing a green test.
	fine := map[string]bool{}
	for _, c := range all {
		for _, cs := range c.Cases {
			if cs.Want == "" {
				continue
			}
			if _, ok := std.KindOf(errs.Code(cs.Want)); !ok {
				fine[cs.Want] = true
			}
		}
	}
	if len(fine) != len(coarsened) {
		t.Fatalf("the corpus speaks %d classes with no declared code, and this test knows %d: %v",
			len(fine), len(coarsened), fine)
	}
	for word := range fine {
		if _, ok := coarsened[word]; !ok {
			t.Fatalf("the corpus speaks %q, which has no declared code and no coarsening — a public code one table row from naming an index", word)
		}
	}

	classified := 0
	for _, engine := range engines {
		for _, cs := range all[engine].Cases {
			if cs.Want == "" || cs.Err == nil {
				continue
			}
			want, ok := coarsened[cs.Want]
			if !ok {
				want = errs.Code(cs.Want)
			}
			got, _, gotOK := sqlerr.Classify(engine, cs.Err)
			if !gotOK {
				t.Errorf("%s/%s: nothing classified %s, which the corpus says is %s",
					engine, cs.Name, cs.Err.Key(), cs.Want)
				continue
			}
			if got != want {
				t.Errorf("%s/%s: classified as %q, and the corpus says %q (code %q)",
					engine, cs.Name, got, cs.Want, want)
			}
			classified++
		}
	}
	if classified == 0 {
		t.Fatal("no case in any corpus was classifiable, so this test asserted nothing")
	}
}

// The failures whose keys do not identify a safe operational code must stay
// unclassified. PostgreSQL, MySQL and MariaDB identify an absent table with a
// dedicated key; SQLite reports only primary result 1 for it, which also covers
// unrelated SQL errors, so guessing from its English message is forbidden.
//
// A parser that classifies everything is worse than one that classifies
// nothing, and the corpus supplies both input and expectation everywhere else —
// so these are what make the positive test above evidence rather than a
// statement about the harness.
func TestTheCorpusNegativesStayUnclassified(t *testing.T) {
	all := corpora(t)

	// The control, first half: an Unreachable row carries no error at all, and
	// SQLite has six of them. A loop over Kind == KindNone would take those as
	// free passes from a parser that knows nothing, so the count of *real*
	// negatives actually walked is asserted per engine, longhand.
	want := map[string]int{"postgres": 2, "mysql": 2, "mariadb": 2, "sqlite": 2}

	for _, engine := range engines {
		walked := 0
		for _, cs := range all[engine].Cases {
			if cs.Want != "" {
				continue
			}
			if cs.Err == nil {
				continue // stated as unreachable; there is nothing to classify
			}
			code, source, ok := sqlerr.Classify(engine, cs.Err)
			if ok {
				t.Errorf("%s/%s: classified %s as %q, and it must stay unclassified",
					engine, cs.Name, cs.Err.Key(), code)
			}
			if code != "" || !isZero(source) {
				t.Errorf("%s/%s: refused and still answered code=%q source=%+v",
					engine, cs.Name, code, source)
			}
			walked++
		}
		if walked != want[engine] {
			t.Errorf("%s: walked %d real negatives, expected %d — a filter that swallowed them would leave this loop empty and green",
				engine, walked, want[engine])
		}
	}

	// The control, second half: a parser returning false for everything passes
	// every assertion above. TestEveryClassifiableCorpusCaseGetsTheCodeItsClassNames
	// is the leg that fails for it, over these same four corpora.
}

// The invariant [[D-039]] states: the key decides, and the text is not part of
// the key.
//
// This is the total, server-free half of the evidence. It reaches all four
// engines and every case, including PostgreSQL and SQLite, which cannot produce
// a localised twin at all — and it is what fails the day somebody reads Detail
// to tell foreign_key from restrict, which is the one temptation §6 names by
// name.
func TestAParserAnswersTheSameWhateverTheServerSaid(t *testing.T) {
	const (
		foreignMessage = "ОШИБКА: значение не прошло проверку"
		foreignDetail  = "Ключ (slug)=(anchor) уже существует."
		foreignHint    = "Попробуйте ещё раз."
	)
	all := corpora(t)

	// The control that the substitution substitutes something: PostgreSQL's
	// three rows a parser would be most tempted to read.
	for _, name := range []string{"unique", "foreign_key", "restrict"} {
		cs := find(t, all["postgres"], name)
		if cs.Err == nil || cs.Err.Fields["Detail"] == "" {
			t.Fatalf("postgres/%s carries no Detail, so the Detail substitution below replaces nothing", name)
		}
	}

	compared, skipped := 0, 0
	for _, engine := range engines {
		classified := 0
		for _, cs := range all[engine].Cases {
			if cs.Err == nil {
				continue
			}
			base := *cs.Err
			wantCode, wantSrc, wantOK := sqlerr.Classify(engine, &base)
			if wantOK {
				classified++
			}

			variants := map[string]sqlerr.Err{}

			if base.Message != foreignMessage {
				v := base
				v.Message = foreignMessage
				variants["a message in another language"] = v
			} else {
				skipped++
			}
			if base.Message != "" {
				v := base
				v.Message = ""
				variants["no message at all"] = v
			} else {
				skipped++
			}
			if base.Fields["Detail"] != foreignDetail || base.Fields["Hint"] != foreignHint {
				v := base
				v.Fields = map[string]string{}
				for k, val := range base.Fields {
					v.Fields[k] = val
				}
				if v.Fields == nil {
					v.Fields = map[string]string{}
				}
				v.Fields["Detail"] = foreignDetail
				v.Fields["Hint"] = foreignHint
				variants["another engine's detail and hint"] = v
			} else {
				skipped++
			}

			for what, v := range variants {
				code, source, ok := sqlerr.Classify(engine, &v)
				if code != wantCode || ok != wantOK || !sameSource(source, wantSrc) {
					t.Errorf("%s/%s with %s classified as (%q, %+v, %v), and as captured it is (%q, %+v, %v) — the text is not part of the key",
						engine, cs.Name, what, code, source, ok, wantCode, wantSrc, wantOK)
				}
				compared++
			}
		}

		// The control, and without it the whole test is green for a Classify
		// that refuses everything: a refusal agrees with a refusal, so all four
		// verdicts match and nothing about the text has been shown. Per engine,
		// because one engine's table deleted leaves that engine's rows agreeing
		// on refusal while the other three carry the file. Not every case
		// classifies — three refusals per engine are corpus negatives — so the
		// assertion is that some did, not that all did.
		if classified == 0 {
			t.Errorf("%s: not one case classified as captured, so its comparisons above are refusals agreeing with refusals",
				engine)
		}
	}
	if compared == 0 {
		t.Fatalf("nothing was substituted at all (%d skips), so this test compared values against themselves", skipped)
	}
}

func sameSource(a, b errs.Source) bool {
	if a.Table != b.Table || a.Schema != b.Schema || a.Constraint != b.Constraint {
		return false
	}
	if len(a.Columns) != len(b.Columns) {
		return false
	}
	if (a.Columns == nil) != (b.Columns == nil) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	return true
}

// The evidence [[D-039]] was owed: the same violation, captured from a server
// answering in Russian, classifies as the English one does.
//
// The invariant itself rests on the test above, which reaches every engine and
// every case. This is what says the invariant is about something real — that
// there exists a server, running now, whose sentence for a duplicate key shares
// not one word with the sentence the parsers were written beside.
func TestTheSameViolationInAnotherLocaleClassifiesIdentically(t *testing.T) {
	all := corpora(t)
	localised := 0

	for _, engine := range engines {
		twin := find(t, all[engine], "unique_in_another_locale")
		if twin.Err == nil {
			if twin.Unreachable == "" {
				t.Errorf("%s: the localised twin has no error and no reason", engine)
			}
			continue
		}
		plain := find(t, all[engine], "unique")
		if plain.Err == nil {
			t.Fatalf("%s has no plain unique case to compare the twin against", engine)
		}

		// The control: two identical sentences prove nothing.
		if twin.Err.Message == plain.Err.Message {
			t.Fatalf("%s: the twin's message is the plain case's, word for word — the locale did not take and this comparison is empty",
				engine)
		}
		localised++

		wantCode, wantSrc, wantOK := sqlerr.Classify(engine, plain.Err)
		// The third control: the English capture has to classify. Two refusals
		// agree as readily as two duplicate-key verdicts do, and a parser that
		// lost this engine's 1062 row would otherwise pass here.
		if !wantOK {
			t.Fatalf("%s: the English duplicate key does not classify, so the twin agreeing with it is one refusal agreeing with another",
				engine)
		}
		code, source, ok := sqlerr.Classify(engine, twin.Err)
		if code != wantCode || ok != wantOK || !sameSource(source, wantSrc) {
			t.Errorf("%s: the Russian capture is (%q, %v) and the English one is (%q, %v)\n  ru: %s\n  en: %s",
				engine, code, ok, wantCode, wantOK, twin.Err.Message, plain.Err.Message)
		}
	}

	// The other half of the control: an lc_messages setting that quietly stopped
	// taking effect would leave every engine's twin unreachable and this test
	// vacuously green.
	if localised < 2 {
		t.Fatalf("only %d engine(s) captured a localised twin; MySQL and MariaDB were both measured to localise a duplicate key", localised)
	}
}

// No parser ever answers one of the four words the corpus speaks and the public
// vocabulary does not.
func TestNoParserAnswersWithTheCorpusFinerVocabulary(t *testing.T) {
	all := corpora(t)
	for _, engine := range engines {
		for _, cs := range all[engine].Cases {
			if cs.Err == nil {
				continue
			}
			code, _, ok := sqlerr.Classify(engine, cs.Err)
			if !ok {
				continue
			}
			if _, fine := coarsened[string(code)]; fine {
				t.Errorf("%s/%s answered %q, which says which index was hit", engine, cs.Name, code)
			}
		}
	}

	// The control. On PostgreSQL and MySQL a primary key already shares its key
	// with an ordinary unique, and on PostgreSQL and SQLite a missing default
	// already shares one with an explicit NULL — so on those engines the
	// coarsening falls out of a parser that never heard of it. Two rows are the
	// only ones where a parser *could* answer the finer word, and both are
	// pinned here on keys the corpus is first asserted to keep distinct.
	sq := all["sqlite"]
	pk, uq := find(t, sq, "primary_key"), find(t, sq, "unique")
	if pk.Err.Native == uq.Err.Native {
		t.Fatalf("SQLite reports a primary key and a unique index as the same %d, so this row proves nothing", pk.Err.Native)
	}
	for _, cs := range []sqlerr.Case{pk, uq} {
		if code, _, _ := sqlerr.Classify("sqlite", cs.Err); code != errs.CodeUnique {
			t.Errorf("sqlite/%s answered %q, want %q", cs.Name, code, errs.CodeUnique)
		}
	}

	for _, engine := range []string{"mysql", "mariadb"} {
		md, nn := find(t, all[engine], "missing_default"), find(t, all[engine], "not_null")
		if md.Err.SQLState == nn.Err.SQLState && md.Err.Native == nn.Err.Native {
			t.Fatalf("%s reports a missing default and an explicit NULL as the same key, so this row proves nothing", engine)
		}
		for _, cs := range []sqlerr.Case{md, nn} {
			if code, _, _ := sqlerr.Classify(engine, cs.Err); code != errs.CodeRequired {
				t.Errorf("%s/%s answered %q, want %q", engine, cs.Name, code, errs.CodeRequired)
			}
		}
	}
}

// A case the corpus calls retryable never comes back as something a client is
// told to fix. [[D-040]]: the same request succeeds unmodified a moment later,
// so a 4xx tells the caller to change something that is not wrong.
//
// This is the only test here whose expectation is not written beside the
// parser. The corpus-Want table is hand-written on both sides, so a
// consistently wrong row — 1205 answering check, which is [[D-040]]'s fourth
// forbid verbatim — passes it and fails here.
func TestARetryableCaseNeverAnswersAConflictOrValidationCode(t *testing.T) {
	std := errs.StandardCodes()
	all := corpora(t)

	for _, engine := range engines {
		retryable := 0
		for _, cs := range all[engine].Cases {
			if cs.Err == nil || cs.Want == "" {
				continue
			}
			code, _, ok := sqlerr.Classify(engine, cs.Err)
			if !ok {
				continue // the positive test above is what fails for this
			}
			kind, known := std.KindOf(code)
			if !known {
				t.Errorf("%s/%s answered %q, which the standard vocabulary does not declare", engine, cs.Name, code)
				continue
			}
			if cs.Kind == sqlerr.KindRetryable {
				if kind != errs.KindRetryable {
					t.Errorf("%s/%s is retryable and answered %q, which is %v", engine, cs.Name, code, kind)
				}
				retryable++
				continue
			}
			// The negative half: without it every assertion above passes for a
			// table answering retryable to everything.
			if kind == errs.KindRetryable {
				t.Errorf("%s/%s is a %s case and answered %q, which is retryable — a caller would be told to try again for something it has to fix",
					engine, cs.Name, cs.Kind, code)
			}
		}
		if retryable == 0 {
			t.Errorf("%s walked no retryable case at all, so nothing above was asserted for it", engine)
		}
	}

	// It deliberately does not cross-check the other three labels. The corpus's
	// Kind is a statement about today's crud.ErrConflict sentinel and §2's
	// status table is a statement about phase 4's Kind, and they disagree by
	// design on required and check.
}

// Only PostgreSQL fills in a Source, because only pgconn carries the fields.
func TestOnlyPostgreSQLFillsInASource(t *testing.T) {
	all := corpora(t)
	pg := all["postgres"]

	uq := find(t, pg, "unique")
	_, source, ok := sqlerr.Classify("postgres", uq.Err)
	if !ok {
		t.Fatal("postgres/unique did not classify")
	}
	if source.Constraint != "cp_parent_slug_key" || source.Table != "cp_parent" || source.Schema != "public" {
		t.Errorf("the source is %+v, and the corpus records constraint cp_parent_slug_key on public.cp_parent", source)
	}
	if source.Columns != nil {
		t.Errorf("a unique violation names no column and the source carries %v", source.Columns)
	}

	nn := find(t, pg, "not_null")
	_, source, _ = sqlerr.Classify("postgres", nn.Err)
	if len(source.Columns) != 1 || source.Columns[0] != "need" {
		t.Errorf("a NOT NULL violation carries ColumnName and the source's columns are %v", source.Columns)
	}

	// The corpus records no fields at all for this one, and an absent column
	// list must not come back reading as "no columns" — errs/build.go's own
	// rule, here at the other end of the same journey.
	tl := find(t, pg, "too_long")
	if tl.Err.Fields != nil {
		t.Fatalf("postgres/too_long now carries fields %v, so it no longer tests the empty case", tl.Err.Fields)
	}
	_, source, _ = sqlerr.Classify("postgres", tl.Err)
	if !isZero(source) {
		t.Errorf("a violation the driver said nothing structural about produced %+v", source)
	}
	if source.Columns != nil {
		t.Errorf("the column list came back as %#v rather than nil, which reads as \"no columns\" instead of \"not known\"", source.Columns)
	}

	// The control: on the other three the driver carries nothing, so a parser
	// that reached into the message for a table name passes the leg above and
	// fails only here.
	for _, engine := range []string{"mysql", "mariadb", "sqlite"} {
		for _, cs := range all[engine].Cases {
			if cs.Err == nil {
				continue
			}
			if _, s, _ := sqlerr.Classify(engine, cs.Err); !isZero(s) {
				t.Errorf("%s/%s produced %+v, and this driver reports no structured fields at all — it can only have come from the message",
					engine, cs.Name, s)
			}
		}
	}
}

func TestAnUnknownDialectAndANilErrorAreRefusedRatherThanPanicking(t *testing.T) {
	entry := find(t, corpora(t)["postgres"], "unique")

	if code, source, ok := sqlerr.Classify("oracle", entry.Err); ok || code != "" || !isZero(source) {
		t.Errorf("a dialect nothing here parses answered (%q, %+v, %v)", code, source, ok)
	}
	if code, source, ok := sqlerr.Classify("postgres", nil); ok || code != "" || !isZero(source) {
		t.Errorf("a nil error answered (%q, %+v, %v)", code, source, ok)
	}

	// The control: without it both assertions pass for a Classify stubbed to
	// return false, and every call site's `if !ok` would look proven.
	if _, _, ok := sqlerr.Classify("postgres", entry.Err); !ok {
		t.Fatal("a real corpus entry through its own dialect did not classify, so the two refusals above say nothing")
	}
}

// The four files ask the same twenty questions, in the same order.
//
// TestTheCorpusStillDescribesTheseServers compares each engine only against its
// own checked-in file, so nothing else in the tree notices a case added to
// three engines and forgotten on the fourth — after which every table test here
// quietly stops covering it there.
func TestEveryEngineAnswersTheSameQuestions(t *testing.T) {
	// Longhand on purpose. Derived from one of the files, this passes for four
	// files that all lost the same case.
	want := []string{
		"unique", "unique_composite", "primary_key", "foreign_key", "restrict",
		"not_null", "check", "missing_default", "too_long", "out_of_range",
		"bad_type", "lock_timeout", "deadlock", "serialization_failure",
		"transaction_aborted", "deferred_constraint", "unique_in_another_locale",
		"undefined_table", "access_denied", "connect_failure",
	}
	for _, engine := range engines {
		c := corpora(t)[engine]
		if len(c.Cases) != len(want) {
			t.Errorf("%s has %d cases and the other engines ask %d questions — run make corpus",
				engine, len(c.Cases), len(want))
			continue
		}
		for i, name := range want {
			if c.Cases[i].Name != name {
				t.Errorf("%s case %d is %q, and every engine's %d'th is %q", engine, i, c.Cases[i].Name, i, name)
			}
		}
	}
}
