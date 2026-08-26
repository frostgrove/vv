package port

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

// parcel carries one of everything the generated artefacts have to reason
// about: a database-generated key, an ordinary column, an insert-only one, the
// optimistic lock, and a column the database fills.
type parcel struct {
	ID        int64     `db:"id,pk,auto"`
	Recipient string    `db:"recipient"`
	Weight    int       `db:"weight"`
	Origin    string    `db:"origin,immutable"`
	Revision  int       `db:"revision,version"`
	Owner     string    `db:"owner"`
	CreatedAt time.Time `db:"created_at,generated"`
}

// what a generator emits for parcel: every column an INSERT writes, less the
// lock, which no client sends.
func parcelPaths() PathMap {
	return PathMap{
		"ID":        At("id"),
		"Recipient": At("recipient"),
		"Weight":    At("weight"),
		"Origin":    At("origin"),
		"Owner":     At("owner"),
	}
}

// the whole writable set…
type parcelUpdate struct {
	Recipient *string
	Weight    *int
	Owner     *string
}

// …and the same DTO with one writable column missing.
type parcelUpdateNarrow struct {
	Recipient *string
	Weight    *int
}

// A declared head is replaced and the rest of the path rides along, so one
// entry covers every violation under a field.
func TestAGeneratedMapRewritesADeclaredHeadAndKeepsTheTail(t *testing.T) {
	// Spare capacity on purpose. A composite literal has none, so a resolver
	// that appended the tail onto the map's own slice would silently reallocate
	// and the write-through check at the bottom would prove nothing.
	to := make(errs.Path, 0, 8)
	to = append(to, errs.Named("shipping"), errs.Named("line1"))
	m := PathMap{"Line1": to}

	got, ok := m.Resolve(errs.Path{errs.Named("Line1"), errs.Indexed(2), errs.Named("Zip")})
	want := errs.Path{errs.Named("shipping"), errs.Named("line1"), errs.Indexed(2), errs.Named("Zip")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("a declared head resolved to %v, %v, want %v", got, ok, want)
	}

	// The declared path is shared by every request that hits this field, so two
	// resolutions must not be able to reach each other. A hop that appended the
	// tail onto the map's own slice writes into the spare capacity above, and
	// the second request then rewrites the first one's last step in place — a
	// corrupted field path under load, which is the worst way for this to fail.
	first, _ := m.Resolve(errs.Path{errs.Named("Line1"), errs.Named("Zip")})
	second, _ := m.Resolve(errs.Path{errs.Named("Line1"), errs.Named("City")})
	if first[2].Name != "Zip" || second[2].Name != "City" {
		t.Fatalf("two resolutions share a backing array: they answered %v and %v", first, second)
	}
}

// The difference between the two types, asserted as a contrast rather than
// described. Same undeclared head, opposite answers — and the hop behind each
// is what says the answers actually differ downstream: a pass-through leaves
// the chain running, a decline stops it and the violation is marked
// approximate.
func TestAGeneratedMapDeclinesWhereFieldsPassesThrough(t *testing.T) {
	in := errs.Path{errs.Named("Email")}

	partial := Fields{"Line1": errs.Path{errs.Named("shipping")}}
	total := PathMap{"Line1": At("shipping")}

	if got, ok := partial.Resolve(in); !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("Fields answered %v, %v for an undeclared head; it must pass one through", got, ok)
	}
	if _, ok := total.Resolve(in); ok {
		t.Fatal("a generated map accepted an undeclared head; a total map that accepts one is guessing")
	}

	behindFields := &recordingHop{prefix: errs.Named("body")}
	if _, ok := errs.Chain(partial, behindFields).Resolve(in); !ok || !behindFields.ran {
		t.Fatal("the hop behind Fields never ran, so passing through and declining are the same thing here")
	}

	behindMap := &recordingHop{prefix: errs.Named("body")}
	out, ok := errs.Chain(total, behindMap).Resolve(in)
	if ok {
		t.Fatal("the chain accepted a path a declining hop refused")
	}
	if behindMap.ran {
		t.Fatal("the hop behind a declining map ran; errs.Chain must stop at the decline")
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("a decline answered %v, want the path as it stood, %v", out, in)
	}
}

// A leading index is a row number, and the field to translate is the step after
// it — which is what phase 7's bulk attribution produces.
//
// Fields deliberately does not do this; TestAnEmptyFieldsMapIsTheIdentity pins
// that side. The divergence is recorded in [[FL-015]].
func TestAGeneratedMapTranslatesUnderALeadingIndex(t *testing.T) {
	m := PathMap{"Email": At("contact", "email")}

	got, ok := m.Resolve(errs.Path{errs.Indexed(3), errs.Named("Email")})
	want := errs.Path{errs.Indexed(3), errs.Named("contact"), errs.Named("email")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("a name under a leading index resolved to %v, %v, want %v", got, ok, want)
	}

	// The negative twin: under the same leading index an undeclared name still
	// declines. Without it, a resolver that answered true for everything under
	// an index would pass the positive case.
	if _, ok := m.Resolve(errs.Path{errs.Indexed(3), errs.Named("Nickname")}); ok {
		t.Fatal("an undeclared name under a leading index was accepted")
	}

	// A path that is only positions names no field, so there is nothing to be
	// approximate about.
	only := errs.Path{errs.Indexed(3)}
	if got, ok := m.Resolve(only); !ok || !reflect.DeepEqual(got, only) {
		t.Fatalf("a path of positions answered %v, %v, want it unchanged and accepted", got, ok)
	}
}

// The start-up refusal, which is what generating the map buys over writing one.
func TestAMapMissingAWritableColumnRefusesAtDeclaration(t *testing.T) {
	full := parcelPaths()
	if _, err := NewPathMap[parcel](full); err != nil {
		t.Fatalf("the complete map was refused: %v", err)
	}

	short := parcelPaths()
	delete(short, "Weight")
	err := errFrom(t, func() { MustPathMap[parcel](short) })
	if err == nil {
		t.Fatal("a map missing a column a client can write was accepted; nothing then catches the drift")
	}
	if !strings.Contains(err.Error(), "Weight") {
		t.Fatalf("the refusal does not name the column: %v", err)
	}

	// An entry with no steps is not an entry — it would answer the field's own
	// tail, which is a path naming nothing.
	empty := parcelPaths()
	empty["Weight"] = errs.Path{}
	if _, err := NewPathMap[parcel](empty); err == nil {
		t.Fatal("an entry with no steps was accepted")
	}
}

// A column the command line took out of the wire shape is declared rather than
// discovered: reflection reads the struct and never the flags.
func TestADeclaredExclusionIsNotRequired(t *testing.T) {
	short := parcelPaths()
	delete(short, "Origin")

	if _, err := NewPathMap[parcel](short, "Origin"); err != nil {
		t.Fatalf("a declared exclusion was still required: %v", err)
	}
	// The control: the same map without the declaration is refused, so an
	// implementation that ignored the list would fail here rather than pass
	// both.
	if _, err := NewPathMap[parcel](short); err == nil {
		t.Fatal("the same missing column was accepted with nothing declared")
	}
}

// The other direction of drift: a column leaves the model and its entry stays.
func TestAnEntryNamingNoColumnIsRefused(t *testing.T) {
	stale := parcelPaths()
	stale["Nickname"] = At("nickname")
	err := errFrom(t, func() { MustPathMap[parcel](stale) })
	if err == nil {
		t.Fatal("an entry naming no column was accepted; a removed column leaves one behind")
	}
	if !strings.Contains(err.Error(), "Nickname") {
		t.Fatalf("the refusal does not name the entry: %v", err)
	}

	// The control: the same map without that entry is accepted.
	if _, err := NewPathMap[parcel](parcelPaths()); err != nil {
		t.Fatalf("the map without the stale entry was refused: %v", err)
	}
}

// The map matches the domain in both directions: total, so no column falls
// through to a guess, and exact, so no entry claims a key nobody sent.
func TestTheMapMustMatchWhatARequestCanCarry(t *testing.T) {
	if _, err := NewPathMap[parcel](parcelPaths()); err != nil {
		t.Fatalf("a map with no entry for the lock or the generated column was refused: %v", err)
	}
	// The control for that: an ordinary column left out of the same map is
	// refused, so this is not a validator that accepts everything.
	short := parcelPaths()
	delete(short, "Owner")
	if _, err := NewPathMap[parcel](short); err == nil {
		t.Fatal("an ordinary missing column was accepted alongside the lock")
	}

	// The other direction. An entry for a column no request carries would
	// translate a violation to a key the client cannot find in its own body,
	// which is as wrong as leaving one out.
	for _, name := range []string{"Revision", "CreatedAt"} {
		claim := parcelPaths()
		claim[name] = At(strings.ToLower(name))
		err := errFrom(t, func() { MustPathMap[parcel](claim) })
		if err == nil {
			t.Fatalf("an entry for %s was accepted; no request carries it", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("the refusal does not name the entry: %v", err)
		}
	}

	// And a column the command line declared out of the wire shape: declaring
	// it and then naming it are contradictory, so the pair is refused too.
	both := parcelPaths()
	if _, err := NewPathMap[parcel](both, "Origin"); err == nil {
		t.Fatal("a declared exclusion with an entry beside it was accepted")
	}
}

// The arm that fires with nothing regenerated: add a column, leave the DTO
// alone, and the compiled artefact refuses to start.
func TestAnUpdateDTOMissingAWritableColumnRefusesAtDeclaration(t *testing.T) {
	if err := CoversUpdate[parcel, parcelUpdate](); err != nil {
		t.Fatalf("the complete DTO was refused: %v", err)
	}

	err := errFrom(t, func() { MustCoverUpdate[parcel, parcelUpdateNarrow]() })
	if err == nil {
		t.Fatal("a DTO missing a writable column was accepted; the column is then silently unpatchable")
	}
	if !strings.Contains(err.Error(), "Owner") {
		t.Fatalf("the refusal does not name the column: %v", err)
	}

	// A column the command line kept out is declared, and accepted.
	if err := CoversUpdate[parcel, parcelUpdateNarrow]("Owner"); err != nil {
		t.Fatalf("a declared exclusion was still required: %v", err)
	}
	// And an exclusion naming no column at all is itself drift.
	if err := CoversUpdate[parcel, parcelUpdate]("Nickname"); err == nil {
		t.Fatal("an exclusion naming no column was accepted")
	}
}

// errFrom runs f and answers the error it panicked with, or nil.
func errFrom(t *testing.T, f func()) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(error)
			if !ok {
				t.Fatalf("the refusal panicked with %T, want an error", r)
			}
			err = e
		}
	}()
	f()
	return nil
}
