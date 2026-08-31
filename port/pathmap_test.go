package port

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

type parcel struct {
	ID        int64     `db:"id,pk,auto"`
	Recipient string    `db:"recipient"`
	Weight    int       `db:"weight"`
	Origin    string    `db:"origin,immutable"`
	Revision  int       `db:"revision,version"`
	Owner     string    `db:"owner"`
	CreatedAt time.Time `db:"created_at,generated"`
}

func parcelPaths() PathMap {
	return PathMap{
		"ID":        At("id"),
		"Recipient": At("recipient"),
		"Weight":    At("weight"),
		"Origin":    At("origin"),
		"Owner":     At("owner"),
	}
}

type parcelUpdate struct {
	Recipient *string
	Weight    *int
	Owner     *string
}

type parcelUpdateNarrow struct {
	Recipient *string
	Weight    *int
}

func TestAGeneratedMapRewritesADeclaredHeadAndKeepsTheTail(t *testing.T) {
	to := make(errs.Path, 0, 8)
	to = append(to, errs.Named("shipping"), errs.Named("line1"))
	m := PathMap{"Line1": to}

	got, ok := m.Resolve(errs.Path{errs.Named("Line1"), errs.Indexed(2), errs.Named("Zip")})
	want := errs.Path{errs.Named("shipping"), errs.Named("line1"), errs.Indexed(2), errs.Named("Zip")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("a declared head resolved to %v, %v, want %v", got, ok, want)
	}

	first, _ := m.Resolve(errs.Path{errs.Named("Line1"), errs.Named("Zip")})
	second, _ := m.Resolve(errs.Path{errs.Named("Line1"), errs.Named("City")})
	if first[2].Name != "Zip" || second[2].Name != "City" {
		t.Fatalf("two resolutions share a backing array: they answered %v and %v", first, second)
	}
}

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

func TestAGeneratedMapTranslatesUnderALeadingIndex(t *testing.T) {
	m := PathMap{"Email": At("contact", "email")}

	got, ok := m.Resolve(errs.Path{errs.Indexed(3), errs.Named("Email")})
	want := errs.Path{errs.Indexed(3), errs.Named("contact"), errs.Named("email")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("a name under a leading index resolved to %v, %v, want %v", got, ok, want)
	}

	if _, ok := m.Resolve(errs.Path{errs.Indexed(3), errs.Named("Nickname")}); ok {
		t.Fatal("an undeclared name under a leading index was accepted")
	}

	only := errs.Path{errs.Indexed(3)}
	if got, ok := m.Resolve(only); !ok || !reflect.DeepEqual(got, only) {
		t.Fatalf("a path of positions answered %v, %v, want it unchanged and accepted", got, ok)
	}
}

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

	empty := parcelPaths()
	empty["Weight"] = errs.Path{}
	if _, err := NewPathMap[parcel](empty); err == nil {
		t.Fatal("an entry with no steps was accepted")
	}
}

func TestADeclaredExclusionIsNotRequired(t *testing.T) {
	short := parcelPaths()
	delete(short, "Origin")

	if _, err := NewPathMap[parcel](short, "Origin"); err != nil {
		t.Fatalf("a declared exclusion was still required: %v", err)
	}

	if _, err := NewPathMap[parcel](short); err == nil {
		t.Fatal("the same missing column was accepted with nothing declared")
	}
}

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

	if _, err := NewPathMap[parcel](parcelPaths()); err != nil {
		t.Fatalf("the map without the stale entry was refused: %v", err)
	}
}

func TestTheMapMustMatchWhatARequestCanCarry(t *testing.T) {
	if _, err := NewPathMap[parcel](parcelPaths()); err != nil {
		t.Fatalf("a map with no entry for the lock or the generated column was refused: %v", err)
	}

	short := parcelPaths()
	delete(short, "Owner")
	if _, err := NewPathMap[parcel](short); err == nil {
		t.Fatal("an ordinary missing column was accepted alongside the lock")
	}

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

	both := parcelPaths()
	if _, err := NewPathMap[parcel](both, "Origin"); err == nil {
		t.Fatal("a declared exclusion with an entry beside it was accepted")
	}
}

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

	if err := CoversUpdate[parcel, parcelUpdateNarrow]("Owner"); err != nil {
		t.Fatalf("a declared exclusion was still required: %v", err)
	}

	if err := CoversUpdate[parcel, parcelUpdate]("Nickname"); err == nil {
		t.Fatal("an exclusion naming no column was accepted")
	}
}

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
