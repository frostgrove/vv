package port

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
)

// A PathMap is the resource adapter's hop of the path chain, inverted: a model
// field name to the key the client sent for it. A generated Mapper answers
// Resolve out of one, which is what makes the adapter able to invert its own
// mapping ([[D-043]]).
//
// It differs from [Fields] in one way, and that difference is the whole reason
// to generate it. Fields is hand-written, so it is partial by nature and an
// undeclared head passes through. A PathMap is derived from the model and
// validated against it at package initialisation, so it is total: every column
// a client can write has an entry. An undeclared head is therefore not a gap in
// the map — it is a column of another table, or one the client never sent a key
// for — and the map declines, which marks the violation approximate. Honest
// beats invented ([[D-050]]).
//
// The zero value declines everything, for the same reason: a map that declares
// nothing has translated nothing.
type PathMap map[string]errs.Path

// At builds the key path a model field maps to. It exists so a generated file
// reads At("authorId") instead of errs.Path{errs.Named("authorId")}.
func At(names ...string) errs.Path {
	p := make(errs.Path, 0, len(names))
	for _, n := range names {
		p = append(p, errs.Named(n))
	}
	return p
}

// Resolve implements errs.Resolver.
func (m PathMap) Resolve(p errs.Path) (errs.Path, bool) {
	// Leading index steps are positions rather than fields, so a bulk write's
	// [0,"Email"] keeps its row number and the step after it is translated.
	// Fields deliberately does not do this: a hand-written map may declare a
	// key called "3" and having it silently ignored would be worse than not
	// looking. A generated map cannot declare an index at all.
	i := 0
	for i < len(p) && p[i].IsIndex {
		i++
	}
	if i == len(p) {
		// Nothing named to translate. A violation at no field — a composite key
		// reported at its common ancestor — must not be marked approximate for
		// having nothing to say.
		return p, true
	}
	to, ok := m[p[i].Name]
	if !ok {
		return p, false
	}
	// A fresh slice. The declared path is shared by every request that hits this
	// field, so appending the tail onto it writes into whatever spare capacity
	// it has, and the next request then rewrites the last step of the one before
	// it — a corrupted field path under load, and only under load.
	out := make(errs.Path, 0, i+len(to)+len(p)-i-1)
	out = append(out, p[:i]...)
	out = append(out, to...)
	out = append(out, p[i+1:]...)
	return out, true
}

// NewPathMap validates m against M and answers it.
//
// The domain is every column an INSERT writes — which includes the primary key,
// because a client-owned key arrives in the create body, and the `immutable`
// insert-only columns, for the same reason. A `generated` column is outside it:
// the client never sends a key for one, so there is nothing to invert.
//
// So is the optimistic lock. The repository advances it ([[D-010]]), no request
// carries it, and a map naming it would be declaring a key nobody sent.
//
// except names the columns the generator was told to leave out of the wire shape
// — `-skip` and `-readonly`. Reflection reads the struct and never the command
// line, so the generated file has to carry that list or this would refuse a
// column its author deliberately dropped.
func NewPathMap[M any](m PathMap, except ...string) (PathMap, error) {
	s, err := crud.SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	skip := setOf(except)
	columns := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		columns[f.Name] = true
	}

	// The domain is exactly what a request can carry, and the map has to match
	// it in both directions. Total, so no column falls through to a guess; and
	// exact, so no entry claims a key nobody sent — an entry for a `generated`
	// column or for the lock would translate a violation to a path the client
	// cannot find in its own body, which is the wrong answer [[D-043]] forbids
	// as firmly as the missing one.
	domain := make(map[string]bool, len(s.Insert))
	for _, f := range s.Insert {
		if f.Version || skip[f.Name] {
			continue
		}
		domain[f.Name] = true
	}

	var missing []string
	for name := range domain {
		// An entry with no steps is not an entry: it would resolve the field to
		// its own tail and hand a client a path naming nothing.
		if to, ok := m[name]; !ok || len(to) == 0 {
			missing = append(missing, name)
		}
	}
	var unknown, outside []string
	for name := range m {
		switch {
		case domain[name]:
		case !columns[name]:
			unknown = append(unknown, name)
		default:
			outside = append(outside, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	sort.Strings(outside)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "no entry for "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		problems = append(problems, "an entry for "+strings.Join(unknown, ", ")+", which the model does not have")
	}
	if len(outside) > 0 {
		problems = append(problems, "an entry for "+strings.Join(outside, ", ")+", which no request carries")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the inverse path map for %s does not match the model: %s — regenerate it, or declare the exclusion",
			s.Name, strings.Join(problems, "; "))
	}
	return m, nil
}

// MustPathMap is [NewPathMap] as a package-level declaration: a map that does
// not cover the model refuses to start rather than answering a wrong path on
// some later request ([[D-021]]).
func MustPathMap[M any](m PathMap, except ...string) PathMap {
	out, err := NewPathMap[M](m, except...)
	if err != nil {
		panic(err)
	}
	return out
}

// CoversUpdate reports whether U names every column of M an UPDATE may write.
// A nil error means it does.
//
// This is the arm that fires with nothing regenerated. Add a column to the
// model, do not run the generator, and the generated file already in the tree
// refuses to start. Regenerate-and-diff cannot catch that — it compares the
// generator against itself — so the two derivations are deliberately
// independent: the generator reads the model's source text, this reads the
// compiled struct ([[D-050]]).
//
// It is checked here and not in sqlrepo.Define, because a hand-narrowed DTO is a
// supported shape and refusing one would break every consumer that has written
// its own. Totality is a property of a generated artefact.
func CoversUpdate[M, U any](except ...string) error {
	s, err := crud.SchemaOf[M]()
	if err != nil {
		return err
	}
	plan, err := crud.PlanFor[U](s)
	if err != nil {
		return err
	}
	covers := plan.Covers()
	covered := make(map[string]bool, len(covers))
	for _, f := range covers {
		covered[f.Name] = true
	}
	skip := setOf(except)
	columns := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		columns[f.Name] = true
	}

	var missing []string
	for _, f := range s.Update {
		if covered[f.Name] || skip[f.Name] {
			continue
		}
		missing = append(missing, f.Name)
	}
	var stale []string
	for name := range skip {
		if !columns[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "no field for "+strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		problems = append(problems, "an exclusion for "+strings.Join(stale, ", ")+", which the model does not have")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the update DTO %s does not cover %s: %s — regenerate it, or declare the exclusion",
		typeName[U](), s.Name, strings.Join(problems, "; "))
}

// MustCoverUpdate is [CoversUpdate] as a package-level declaration. The
// generated file calls it from init, so a column the DTO does not cover is a
// start-up refusal rather than a column updates silently cannot reach.
func MustCoverUpdate[M, U any](except ...string) {
	if err := CoversUpdate[M, U](except...); err != nil {
		panic(err)
	}
}

func setOf(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// typeName names U for the refusal message. The DTO type is the thing whose
// author has to act, so it is the thing the panic has to name.
func typeName[T any]() string {
	var zero T
	return reflect.TypeOf(&zero).Elem().String()
}
