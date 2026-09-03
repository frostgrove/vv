package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultWirePkg = "github.com/frostgrove/vv/crud/wire"

type ResourceOptions struct {
	Dir       string
	Out       string
	Manifest  string
	Types     string
	Skip      string
	Readonly  string
	Into      string
	Import    string
	Recursive bool
	Check     bool
	CrudPkg   string
	UtilsPkg  string
	PortPkg   string
	WirePkg   string
	Log       io.Writer
}

type resourceBodies struct {
	create   []field
	patch    []field
	response []field
}

func RunResource(o *ResourceOptions) error {
	if o == nil {
		return fmt.Errorf("codegen: options are nil")
	}
	outName := cmpOr(o.Out, "vv_wire_gen.go")
	if err := artifactName(outName, "-out"); err != nil {
		return err
	}
	manifestName := cmpOr(o.Manifest, "resource.manifest.yml")
	if err := artifactName(manifestName, "-manifest"); err != nil {
		return err
	}
	if o.Recursive {
		if o.Into != "" || o.Import != "" {
			return fmt.Errorf("-recursive writes beside each model package and cannot be combined with -into or -import")
		}
		dirs, err := modelDirs(o.Dir)
		if err != nil {
			return err
		}
		var waiting, stale []string
		for _, dir := range dirs {
			one := *o
			one.Dir, one.Out, one.Manifest, one.Recursive = dir, outName, manifestName, false
			err := RunResource(&one)
			var confirmation *ConfirmationError
			var drift *DriftError
			switch {
			case err == nil:
			case errors.As(err, &confirmation):
				for _, body := range confirmation.Bodies {
					waiting = append(waiting, filepath.Base(dir)+"."+body)
				}
			case errors.As(err, &drift):
				stale = append(stale, drift.Paths...)
			case strings.Contains(err.Error(), "no models found in "):
			default:
				return err
			}
		}
		if len(waiting) != 0 {
			sort.Strings(waiting)
			return &ConfirmationError{Manifest: manifestName, Bodies: waiting}
		}
		if len(stale) != 0 {
			sort.Strings(stale)
			return &DriftError{Paths: stale}
		}
		return nil
	}

	g := &generator{
		dir:      o.Dir,
		depth:    1,
		wireOnly: true,
		binding:  "none",
		specsPkg: DefaultSpecsPkg,
		crudPkg:  cmpOr(o.CrudPkg, DefaultCrudPkg),
		utilsPkg: cmpOr(o.UtilsPkg, DefaultUtilsPkg),
		portPkg:  cmpOr(o.PortPkg, DefaultPortPkg),
		errsPkg:  DefaultErrsPkg,
		netPkg:   DefaultNetPkg,
		wirePkg:  cmpOr(o.WirePkg, DefaultWirePkg),
		into:     o.Into,
		skip:     names(o.Skip),
		readonly: names(o.Readonly),
		log:      o.Log,
	}
	g.modelImport = o.Import
	if o.Types != "" {
		g.only = map[string]bool{}
		for _, name := range strings.Split(o.Types, ",") {
			g.only[strings.TrimSpace(name)] = true
		}
	}
	outDir := cmpOr(o.Into, o.Dir)
	outPath, err := containedOutputPath(outDir, outName)
	if err != nil {
		return err
	}
	manifestPath, err := containedOutputPath(outDir, manifestName)
	if err != nil {
		return err
	}
	return g.runWire(outPath, manifestPath, o.Check)
}

func artifactName(name, flag string) error {
	if filepath.IsAbs(name) || filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("codegen: %s must be a file name without directories, got %q", flag, name)
	}
	return nil
}

func (this *generator) runWire(outPath, manifestPath string, check bool) error {
	if err := validateGeneratedTarget(outPath); err != nil {
		return err
	}
	if err := this.load(outPath); err != nil {
		return err
	}
	if this.into != "" {
		if this.modelImport == "" {
			return fmt.Errorf("-into needs -import so the generated file can name the model types")
		}
		if err := os.MkdirAll(this.into, 0o755); err != nil {
			return err
		}
		this.pkg = packageNameOf(this.into)
	}
	if len(this.order) == 0 {
		return fmt.Errorf("no models found in %s; put exported model structs in model.go, *.model.go or *_model.go", this.dir)
	}
	prior, err := readResourceManifest(manifestPath, this.pkg)
	if err != nil {
		return err
	}
	document, err := this.buildManifest(prior, filepath.Base(manifestPath))
	if err != nil {
		return err
	}
	manifestSource, err := marshalManifest(document)
	if err != nil {
		return err
	}
	unconfirmed := unconfirmedBodies(document)
	if len(unconfirmed) != 0 {
		if !check {
			if err := writeArtifact(manifestPath, manifestSource, validateManifestTarget); err != nil {
				return err
			}
		}
		return &ConfirmationError{Manifest: filepath.Base(manifestPath), Bodies: unconfirmed}
	}
	if err := this.validateDeclarations(outPath); err != nil {
		return err
	}
	source, err := this.render()
	if err != nil {
		return err
	}
	if err := this.validateRenderedImports(outPath, source); err != nil {
		return err
	}
	if check {
		return checkArtifacts([]artifact{{outPath, source}, {manifestPath, manifestSource}})
	}
	if err := writeArtifact(manifestPath, manifestSource, validateManifestTarget); err != nil {
		return err
	}
	if err := writeGenerated(outPath, source); err != nil {
		return err
	}
	if this.log != nil {
		fmt.Fprintf(this.log, "vv: wrote %s and %s (%d resources)\n", outPath, manifestPath, len(this.order))
	}
	return nil
}

type artifact struct {
	path string
	want []byte
}

func checkArtifacts(artifacts []artifact) error {
	var stale []string
	for _, item := range artifacts {
		actual, err := os.ReadFile(item.path)
		if err != nil || !bytes.Equal(actual, item.want) {
			stale = append(stale, item.path)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return &DriftError{Paths: stale}
}

func (this *generator) buildManifest(prior *manifestDocument, manifestName string) (manifestDocument, error) {
	document := manifestDocument{
		Format:      manifestFormat,
		GeneratedBy: manifestGeneratedBy,
		Package:     this.pkg,
		Resources:   make([]manifestResource, 0, len(this.order)),
	}
	previous := map[string]manifestResource{}
	if prior != nil {
		for _, resource := range prior.Resources {
			previous[resource.Model] = resource
		}
	}
	this.bodies = map[string]resourceBodies{}
	for _, name := range this.order {
		m := this.models[name]
		entry := manifestResource{Model: m.Name}
		known, hadPrior := previous[m.Name]
		for _, body := range []struct {
			name        string
			narrowed    []field
			publishable []field
			into        *manifestBody
		}{
			{"create", narrowedCreateFields(m), publishableCreateFields(m), &entry.Create},
			{"patch", narrowedPatchFields(m), publishablePatchFields(m), &entry.Patch},
			{"response", narrowedResponseFields(m), publishableResponseFields(m), &entry.Response},
		} {
			var carried *manifestBody
			if hadPrior {
				previousBody := known.body(body.name)
				carried = &previousBody
			}
			merged, err := mergeManifestBody(fieldNames(body.narrowed), fieldNames(body.publishable), carried)
			if err != nil {
				return manifestDocument{}, fmt.Errorf("codegen: %s names %s %s: %w", manifestName, m.Name, body.name, err)
			}
			*body.into = merged
		}
		document.Resources = append(document.Resources, entry)
		this.bodies[m.Name] = resourceBodies{
			create:   selectFields(publishableCreateFields(m), entry.Create.Fields),
			patch:    selectFields(publishablePatchFields(m), entry.Patch.Fields),
			response: selectFields(publishableResponseFields(m), entry.Response.Fields),
		}
	}
	return document, nil
}

func fieldNames(fields []field) []string {
	out := make([]string, 0, len(fields))
	for _, item := range fields {
		out = append(out, item.Name)
	}
	sort.Strings(out)
	return out
}

func selectFields(available []field, published []string) []field {
	keep := setOf(published)
	out := make([]field, 0, len(published))
	for _, item := range available {
		if keep[item.Name] {
			out = append(out, item)
		}
	}
	return out
}

func excludedFrom(available, published []field) []string {
	kept := setOf(fieldNames(published))
	out := []string{}
	for _, item := range available {
		if !kept[item.Name] {
			out = append(out, item.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (this field) column() bool { return !this.isRelation() && this.Tag != "-" }

func publishableCreateFields(m *model) []field {
	var out []field
	for _, f := range m.Fields {
		if f.column() && !f.Generated && !f.ServerOwned {
			out = append(out, f)
		}
	}
	return out
}

func narrowedCreateFields(m *model) []field {
	var out []field
	for _, f := range inputFields(m) {
		if !f.Secret {
			out = append(out, f)
		}
	}
	return out
}

func publishablePatchFields(m *model) []field { return updateFields(m) }

func narrowedPatchFields(m *model) []field {
	var out []field
	for _, f := range updateFields(m) {
		if !f.Secret {
			out = append(out, f)
		}
	}
	return out
}

func publishableResponseFields(m *model) []field {
	var out []field
	for _, f := range m.Fields {
		if f.column() {
			out = append(out, f)
		}
	}
	return out
}

func narrowedResponseFields(m *model) []field {
	var out []field
	for _, f := range m.Fields {
		if f.column() && !f.Skip && !f.Secret {
			out = append(out, f)
		}
	}
	return out
}

func (this *generator) renderWire(m *model) (string, used, error) {
	bodies, known := this.bodies[m.Name]
	if !known {
		return "", used{}, fmt.Errorf("codegen: no wire bodies were derived for %s", m.Name)
	}
	model := this.qual(m.Name)
	u := used{context: true, port: true, wire: true}

	var b strings.Builder
	this.renderWireStruct(&b, m.Name+"Input", bodies.create, "", &u)
	fmt.Fprintf(&b, "type %sInputMapper struct{}\n\n", m.Name)
	fmt.Fprintf(&b, "func (%sInputMapper) Model(_ context.Context, in %sInput) (%s, error) {\n", m.Name, m.Name, model)
	fmt.Fprintf(&b, "\tout := %s{}\n", model)
	for _, f := range bodies.create {
		fmt.Fprintf(&b, "\tout.%s = in.%s\n", f.Name, f.Name)
	}
	b.WriteString("\treturn out, nil\n}\n\n")
	fmt.Fprintf(&b, "var _ port.Mapper[%sInput, %s] = %sInputMapper{}\n\n", m.Name, model, m.Name)

	this.renderWireStruct(&b, m.Name+"Patch", bodies.patch, "omitempty", &u)
	fmt.Fprintf(&b, "type %sPatchMapper struct{}\n\n", m.Name)
	fmt.Fprintf(&b, "func (%sPatchMapper) Update(patch %sPatch) %sUpdate {\n", m.Name, m.Name, m.Name)
	fmt.Fprintf(&b, "\tout := %sUpdate{}\n", m.Name)
	for _, f := range bodies.patch {
		fmt.Fprintf(&b, "\tout.%s = patch.%s\n", f.Name, f.Name)
	}
	b.WriteString("\treturn out\n}\n\n")
	fmt.Fprintf(&b, "var _ wire.PatchMapper[%sPatch, %sUpdate] = %sPatchMapper{}\n\n", m.Name, m.Name, m.Name)

	this.renderWireStruct(&b, m.Name+"Response", bodies.response, "", &u)
	fmt.Fprintf(&b, "type %sPresenter struct{}\n\n", m.Name)
	fmt.Fprintf(&b, "func (%sPresenter) Response(model %s) %sResponse {\n", m.Name, model, m.Name)
	fmt.Fprintf(&b, "\tout := %sResponse{}\n", m.Name)
	for _, f := range bodies.response {
		fmt.Fprintf(&b, "\tout.%s = model.%s\n", f.Name, f.Name)
	}
	b.WriteString("\treturn out\n}\n\n")
	fmt.Fprintf(&b, "var _ wire.Presenter[%s, %sResponse] = %sPresenter{}\n\n", model, m.Name, m.Name)

	return b.String(), u, nil
}

func (this *generator) renderWireStruct(b *strings.Builder, name string, fields []field, omit string, u *used) {
	fmt.Fprintf(b, "type %s struct {\n", name)
	for _, f := range fields {
		typ := f.Type
		if omit != "" {
			typ = dtoType(f.Type)
		}
		tag := lowerFirst(f.Name)
		if omit != "" {
			tag += ","
			if strings.HasPrefix(typ, "utils.Opt[") {
				tag += "omitzero"
			} else {
				tag += omit
			}
		}
		if strings.Contains(typ, "crud.Opt[") {
			u.crud = true
		}
		if strings.Contains(typ, "utils.Opt[") {
			u.utils = true
		}
		if strings.Contains(typ, "time.Time") {
			u.time = true
		}
		fmt.Fprintf(b, "\t%s %s `json:%q`\n", f.Name, typ, tag)
	}
	b.WriteString("}\n\n")
}

func (this *generator) renderWireCoverage() string {
	var b strings.Builder
	b.WriteString("func init() {\n")
	for _, name := range this.order {
		m := this.models[name]
		bodies := this.bodies[m.Name]
		fmt.Fprintf(&b, "\twire.MustCoverCreate[%s, %sInput](%s)\n",
			this.qual(m.Name), m.Name, quoteList(excludedFrom(publishableCreateFields(m), bodies.create)))
		fmt.Fprintf(&b, "\twire.MustCoverPatch[%sUpdate, %sPatch](%s)\n",
			m.Name, m.Name, quoteList(excludedFrom(publishablePatchFields(m), bodies.patch)))
		fmt.Fprintf(&b, "\twire.MustCoverResponse[%s, %sResponse](%s)\n",
			this.qual(m.Name), m.Name, quoteList(excludedFrom(publishableResponseFields(m), bodies.response)))
	}
	b.WriteString("}\n\n")
	return b.String()
}
