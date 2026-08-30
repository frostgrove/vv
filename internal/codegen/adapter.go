package codegen

import (
	"fmt"
	"sort"
	"strings"
)

// The adapter half of the output: the transport's own entity body, the mapper
// that turns it into the model, the inverse of that mapping, the service shell
// and the wiring.
//
// It lives beside render.go rather than in it so the DTO and metamodel half
// stays readable, and so [[FL-010]] can name the two halves separately.

// inputFields are the columns a client may send in an entity body: everything
// but the relations, the columns the database fills, the lock the repository
// advances, and whatever the command line took out of the wire shape.
//
// It is the same set port.NewPathMap validates against — Schema.Insert less the
// version column and the declared exclusions — derived the other way round. The
// two agreeing is what the start-up check measures.
func inputFields(m *model) []field {
	var out []field
	for _, f := range m.Fields {
		if f.Skip || f.isRelation() || f.Generated || f.Version || f.Excluded || (f.PK && f.Auto) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// inputExclusions are columns reflection still sees in Schema.Insert but this
// generated wire body deliberately does not carry. Command-line exclusions are
// joined by an auto-generated key: a client-owned assigned key remains part of
// the body, while an identity/serial key belongs to the database and the path.
func inputExclusions(m *model) []string {
	out := append([]string(nil), m.excluded()...)
	seen := make(map[string]bool, len(out)+1)
	for _, name := range out {
		seen[name] = true
	}
	for _, f := range m.Fields {
		if f.PK && f.Auto && !seen[f.Name] {
			out = append(out, f.Name)
			seen[f.Name] = true
		}
	}
	sort.Strings(out)
	return out
}

func (this *generator) renderAdapter(m *model) (string, used, error) {
	pk, ok := m.pk()
	if !ok {
		return "", used{}, fmt.Errorf(`-adapter needs a key it can name: tag one field of %s db:",pk"`, m.Name)
	}
	id, _ := elem(pk.Type)
	fields := inputFields(m)
	if len(fields) == 0 {
		return "", used{}, fmt.Errorf("-adapter found no column a client can send on %s", m.Name)
	}

	var b strings.Builder
	u := used{port: true, errs: true, context: true}
	model := this.qual(m.Name)

	// ---- the entity body
	fmt.Fprintf(&b, "// %sInput is the entity body for %s: what a create or a replace carries,\n", m.Name, model)
	fmt.Fprintf(&b, "// under this resource's own wire names. %sUpdate is the PATCH shape; a\n", m.Name)
	fmt.Fprintf(&b, "// partial update has to tell absent from null and an entity body does not.\n")
	fmt.Fprintf(&b, "type %sInput struct {\n", m.Name)
	for _, f := range fields {
		if strings.Contains(f.Type, "crud.Opt[") {
			u.crud = true
		}
		if strings.Contains(f.Type, "utils.Opt[") {
			u.utils = true
		}
		if strings.Contains(f.Type, "time.Time") {
			u.time = true
		}
		fmt.Fprintf(&b, "\t%s %s `json:%q`\n", f.Name, f.Type, lowerFirst(f.Name))
	}
	b.WriteString("}\n\n")

	// ---- the mapper
	fmt.Fprintf(&b, "// %sMapper turns %sInput into %s, and inverts that mapping for the\n", m.Name, m.Name, model)
	fmt.Fprintf(&b, "// path chain. It performed the mapping, so it is the only layer that can\n")
	fmt.Fprintf(&b, "// invert it ([[D-043]]).\n")
	fmt.Fprintf(&b, "type %sMapper struct{}\n\n", m.Name)

	fmt.Fprintf(&b, "// Model implements port.Mapper.\n")
	fmt.Fprintf(&b, "func (%sMapper) Model(_ context.Context, in %sInput) (%s, error) {\n", m.Name, m.Name, model)
	fmt.Fprintf(&b, "\tout := %s{}\n", model)
	for _, f := range fields {
		// Assignment, unlike a keyed composite literal, also reaches a field
		// promoted from a flattened anonymous mixin. Runtime metadata sees that
		// promoted field as a column, so the generated mapper must be able to set it.
		fmt.Fprintf(&b, "\tout.%s = in.%s\n", f.Name, f.Name)
	}
	b.WriteString("\treturn out, nil\n}\n\n")

	fmt.Fprintf(&b, "// Resolve implements errs.Resolver, which is what puts this hop ahead of\n")
	fmt.Fprintf(&b, "// the raw-body fallback: a declared mapping always beats a guess.\n")
	fmt.Fprintf(&b, "func (%sMapper) Resolve(p errs.Path) (errs.Path, bool) { return %sPaths.Resolve(p) }\n\n", m.Name, m.Name)

	// ---- the inverse map
	fmt.Fprintf(&b, "// %sPaths is the inverse of %sMapper: a model field name to the key the\n", m.Name, m.Name)
	fmt.Fprintf(&b, "// client sent for it. It is checked against the model at package\n")
	fmt.Fprintf(&b, "// initialisation, so a column it does not cover refuses to start rather than\n")
	fmt.Fprintf(&b, "// answering a wrong path on some later request ([[D-021]], [[D-050]]).\n")
	fmt.Fprintf(&b, "var %sPaths = port.MustPathMap[%s](port.PathMap{\n", m.Name, model)
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%q: port.At(%q),\n", f.Name, lowerFirst(f.Name))
	}
	if ex := inputExclusions(m); len(ex) > 0 {
		fmt.Fprintf(&b, "}, %s)\n\n", quoteList(ex))
	} else {
		b.WriteString("})\n\n")
	}

	// ---- the service shell
	fmt.Fprintf(&b, "// %sService is the service shell: the default orchestration, and somewhere to\n", m.Name)
	fmt.Fprintf(&b, "// override one method without writing the other eight.\n")
	fmt.Fprintf(&b, "type %sService struct {\n\t*port.DefaultService[%s, %s, %sUpdate]\n}\n\n", m.Name, model, id, m.Name)

	fmt.Fprintf(&b, "// An override that changes a signature is a build failure here rather than\n")
	fmt.Fprintf(&b, "// a service that quietly no longer mounts.\n")
	fmt.Fprintf(&b, "var _ port.Service[%s, %s, %sUpdate] = (*%sService)(nil)\n\n", model, id, m.Name, m.Name)

	fmt.Fprintf(&b, "// New%sService builds the service over a repository.\n", m.Name)
	fmt.Fprintf(&b, "func New%sService(repo port.Repository[%s, %s, %sUpdate], opts ...port.ServiceOption) *%sService {\n",
		m.Name, model, id, m.Name, m.Name)
	fmt.Fprintf(&b, "\treturn &%sService{DefaultService: port.NewService(repo, opts...)}\n}\n\n", m.Name)

	// ---- the wiring
	if this.binding == "net" {
		u.http, u.net = true, true
		fmt.Fprintf(&b, "// Mount%s mounts the resource on a ServeMux under prefix, behind %sMapper.\n", m.Name, m.Name)
		fmt.Fprintf(&b, "//\n")
		fmt.Fprintf(&b, "// It takes a service rather than a repository, so a hand-written one slots in\n")
		fmt.Fprintf(&b, "// and the service-shaped options a Serving constructor refuses cannot be handed\n")
		fmt.Fprintf(&b, "// to it by mistake ([[D-021]]).\n")
		fmt.Fprintf(&b, "func Mount%s(mux *http.ServeMux, prefix string, svc port.Service[%s, %s, %sUpdate], opts ...crudnet.Option[%s, %s, %sUpdate]) {\n",
			m.Name, model, id, m.Name, model, id, m.Name)
		fmt.Fprintf(&b, "\tcrudnet.ServingFor(svc, %sMapper{}, opts...).Mount(mux, prefix)\n}\n\n", m.Name)
	}

	return b.String(), u, nil
}

// renderCoverage is the half that ships whether or not -adapter is on. It is
// what makes a new column a start-up refusal for every consumer rather than
// only for the ones that generate an adapter.
func (this *generator) renderCoverage() string {
	var b strings.Builder
	b.WriteString("// A writable column the update DTO does not name refuses to start, rather than\n")
	b.WriteString("// becoming a column updates silently cannot reach ([[D-050]]). The generator\n")
	b.WriteString("// read the model's source text and this reads the compiled struct, so the two\n")
	b.WriteString("// can drift apart — and this is what says so when they do, with nothing\n")
	b.WriteString("// regenerated.\n")
	b.WriteString("func init() {\n")
	for _, name := range this.order {
		m := this.models[name]
		fmt.Fprintf(&b, "\tport.MustCoverUpdate[%s, %sUpdate](%s)\n", this.qual(m.Name), m.Name, quoteList(m.excluded()))
	}
	b.WriteString("}\n\n")
	return b.String()
}

// quoteList renders a Go argument list of string literals.
func quoteList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return strings.Join(out, ", ")
}
