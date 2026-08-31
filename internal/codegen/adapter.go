package codegen

import (
	"fmt"
	"sort"
	"strings"
)

func inputFields(m *model) []field {
	var out []field
	for _, f := range m.Fields {
		if f.Skip || f.isRelation() || f.Generated || f.ServerOwned || f.Tombstone || f.Version || f.Excluded || (f.PK && f.Auto) {
			continue
		}
		out = append(out, f)
	}
	return out
}

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

	var b strings.Builder
	u := used{port: true, errs: true, context: true}
	model := this.qual(m.Name)

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

	fmt.Fprintf(&b, "type %sMapper struct{}\n\n", m.Name)

	fmt.Fprintf(&b, "func (%sMapper) Model(_ context.Context, in %sInput) (%s, error) {\n", m.Name, m.Name, model)
	fmt.Fprintf(&b, "\tout := %s{}\n", model)
	for _, f := range fields {
		fmt.Fprintf(&b, "\tout.%s = in.%s\n", f.Name, f.Name)
	}
	b.WriteString("\treturn out, nil\n}\n\n")

	fmt.Fprintf(&b, "func (%sMapper) Resolve(p errs.Path) (errs.Path, bool) { return %sPaths.Resolve(p) }\n\n", m.Name, m.Name)

	fmt.Fprintf(&b, "var %sPaths = port.MustPathMap[%s](port.PathMap{\n", m.Name, model)
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%q: port.At(%q),\n", f.Name, lowerFirst(f.Name))
	}
	if ex := inputExclusions(m); len(ex) > 0 {
		fmt.Fprintf(&b, "}, %s)\n\n", quoteList(ex))
	} else {
		b.WriteString("})\n\n")
	}

	fmt.Fprintf(&b, "type %sService struct {\n\t*port.DefaultService[%s, %s, %sUpdate]\n}\n\n", m.Name, model, id, m.Name)

	fmt.Fprintf(&b, "var _ port.Service[%s, %s, %sUpdate] = (*%sService)(nil)\n\n", model, id, m.Name, m.Name)

	fmt.Fprintf(&b, "func New%sService(repo port.Repository[%s, %s, %sUpdate], opts ...port.ServiceOption) *%sService {\n",
		m.Name, model, id, m.Name, m.Name)
	fmt.Fprintf(&b, "\treturn &%sService{DefaultService: port.NewService(repo, opts...)}\n}\n\n", m.Name)

	if this.binding == "net" {
		u.http, u.net = true, true
		fmt.Fprintf(&b, "func Mount%s(mux *http.ServeMux, prefix string, svc port.Service[%s, %s, %sUpdate], opts ...crudnet.Option[%s, %s, %sUpdate]) {\n",
			m.Name, model, id, m.Name, model, id, m.Name)
		fmt.Fprintf(&b, "\tcrudnet.ServingFor(svc, %sMapper{}, opts...).Mount(mux, prefix)\n}\n\n", m.Name)
	}

	return b.String(), u, nil
}

func (this *generator) renderCoverage() string {
	var b strings.Builder
	b.WriteString("func init() {\n")
	for _, name := range this.order {
		m := this.models[name]
		fmt.Fprintf(&b, "\tport.MustCoverUpdate[%s, %sUpdate](%s)\n", this.qual(m.Name), m.Name, quoteList(m.excluded()))
	}
	b.WriteString("}\n\n")
	return b.String()
}

func quoteList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return strings.Join(out, ", ")
}
