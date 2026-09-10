package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/constant"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type listedPackage struct {
	ImportPath string
	Export     string
	DepOnly    bool
	Standard   bool
	Error      *struct {
		Err string
	}
	DepsErrors []struct {
		Err string
	}
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output io.Writer) error {
	packages, targets, err := listPackages()
	if err != nil {
		return err
	}
	lookup := func(path string) (io.ReadCloser, error) {
		listed, ok := packages[path]
		if !ok || listed.Export == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(listed.Export)
	}
	loader := importer.ForCompiler(token.NewFileSet(), runtime.Compiler, lookup)
	sections := make([]string, 0, len(targets))
	for _, path := range targets {
		pkg, err := loader.Import(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		body := renderPackage(pkg)
		if body == "" {
			continue
		}
		sections = append(sections, "## "+path+"\n```go\n"+body+"\n```")
	}
	_, err = io.WriteString(output, strings.Join(sections, "\n\n"))
	return err
}

func listPackages() (map[string]listedPackage, []string, error) {
	command := exec.Command("go", "list", "-deps", "-export", "-json", "./...")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	packages := make(map[string]listedPackage)
	var targets []string
	decoder := json.NewDecoder(stdout)
	for {
		var listed listedPackage
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = command.Wait()
			return nil, nil, fmt.Errorf("decode go list output: %w", err)
		}
		packages[listed.ImportPath] = listed
		if !listed.DepOnly && !listed.Standard && listed.Export != "" && !internalPackage(listed.ImportPath) {
			targets = append(targets, listed.ImportPath)
		}
	}
	if err := command.Wait(); err != nil {
		return nil, nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	slices.Sort(targets)
	return packages, targets, nil
}

func internalPackage(path string) bool {
	return strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal")
}

func renderPackage(pkg *types.Package) string {
	qualify := func(other *types.Package) string {
		if other == nil || other == pkg {
			return ""
		}
		return other.Path()
	}
	var declarations []string
	for _, name := range pkg.Scope().Names() {
		object := pkg.Scope().Lookup(name)
		if object == nil || !object.Exported() {
			continue
		}
		switch object := object.(type) {
		case *types.Const:
			declarations = append(declarations, renderConstant(object, qualify))
		case *types.Var:
			declarations = append(declarations, "var "+object.Name()+" "+types.TypeString(object.Type(), qualify))
		case *types.Func:
			declarations = append(declarations, "func "+object.Name()+renderSignature(object.Signature(), qualify))
		case *types.TypeName:
			declarations = append(declarations, renderType(object, qualify)...)
		}
	}
	return strings.Join(declarations, "\n")
}

func renderConstant(value *types.Const, qualify types.Qualifier) string {
	exact := value.Val().ExactString()
	if value.Val().Kind() == constant.String {
		exact = strconv.Quote(constant.StringVal(value.Val()))
	}
	return "const " + value.Name() + " " + types.TypeString(value.Type(), qualify) + " = " + exact
}

func renderType(object *types.TypeName, qualify types.Qualifier) []string {
	if object.IsAlias() {
		alias, _ := object.Type().(*types.Alias)
		if alias == nil {
			return []string{"type " + object.Name() + " = " + types.TypeString(object.Type(), qualify)}
		}
		name := object.Name() + renderTypeParameters(alias.TypeParams(), qualify)
		declaration := "type " + name + " = " + types.TypeString(alias.Rhs(), qualify)
		receiver := object.Name() + renderTypeArguments(alias.TypeParams())
		return append([]string{declaration}, renderMethodSets(alias, receiver, qualify)...)
	}
	named, ok := object.Type().(*types.Named)
	if !ok {
		return []string{"type " + object.Name() + " " + types.TypeString(object.Type().Underlying(), qualify)}
	}
	name := object.Name() + renderTypeParameters(named.TypeParams(), qualify)
	var declaration string
	switch underlying := named.Underlying().(type) {
	case *types.Struct:
		declaration = renderStruct(name, underlying, qualify)
	case *types.Interface:
		declaration = renderInterface(name, underlying, qualify)
	default:
		declaration = "type " + name + " " + types.TypeString(underlying, qualify)
	}
	return append([]string{declaration}, renderMethods(named, qualify)...)
}

func renderTypeParameters(parameters *types.TypeParamList, qualify types.Qualifier) string {
	if parameters == nil || parameters.Len() == 0 {
		return ""
	}
	values := make([]string, parameters.Len())
	for index := range values {
		parameter := parameters.At(index)
		values[index] = parameter.Obj().Name() + " " + types.TypeString(parameter.Constraint(), qualify)
	}
	return "[" + strings.Join(values, ", ") + "]"
}

func renderStruct(name string, structure *types.Struct, qualify types.Qualifier) string {
	fields := make([]string, 0, structure.NumFields()+1)
	hidden := false
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() {
			hidden = true
			continue
		}
		line := field.Name() + " " + types.TypeString(field.Type(), qualify)
		if field.Embedded() {
			line = types.TypeString(field.Type(), qualify)
		}
		if tag := structure.Tag(index); tag != "" {
			line += " " + strconv.Quote(tag)
		}
		fields = append(fields, line)
	}
	if hidden {
		fields = append(fields, "<unexported fields>")
	}
	if len(fields) == 0 {
		return "type " + name + " struct {}"
	}
	return "type " + name + " struct {\n\t" + strings.Join(fields, "\n\t") + "\n}"
}

func renderInterface(name string, contract *types.Interface, qualify types.Qualifier) string {
	contract.Complete()
	values := make([]string, 0, contract.NumEmbeddeds()+contract.NumExplicitMethods()+1)
	for index := 0; index < contract.NumEmbeddeds(); index++ {
		values = append(values, types.TypeString(contract.EmbeddedType(index), qualify))
	}
	hidden := false
	methods := make([]string, 0, contract.NumExplicitMethods())
	for index := 0; index < contract.NumExplicitMethods(); index++ {
		method := contract.ExplicitMethod(index)
		if !method.Exported() {
			hidden = true
			continue
		}
		methods = append(methods, method.Name()+renderSignature(method.Signature(), qualify))
	}
	sort.Strings(methods)
	values = append(values, methods...)
	if hidden {
		values = append(values, "<unexported methods>")
	}
	if len(values) == 0 {
		return "type " + name + " interface {}"
	}
	return "type " + name + " interface {\n\t" + strings.Join(values, "\n\t") + "\n}"
}

func renderMethods(named *types.Named, qualify types.Qualifier) []string {
	if _, ok := named.Underlying().(*types.Interface); ok {
		return nil
	}
	receiver := named.Obj().Name() + renderTypeArguments(named.TypeParams())
	return renderMethodSets(named, receiver, qualify)
}

func renderMethodSets(value types.Type, receiver string, qualify types.Qualifier) []string {
	valueMethods := exportedMethods(types.NewMethodSet(value), qualify)
	pointerMethods := exportedMethods(types.NewMethodSet(types.NewPointer(value)), qualify)
	for name := range valueMethods {
		delete(pointerMethods, name)
	}
	lines := make([]string, 0, len(valueMethods)+len(pointerMethods))
	for name, signature := range valueMethods {
		lines = append(lines, "func ("+receiver+") "+name+signature)
	}
	for name, signature := range pointerMethods {
		lines = append(lines, "func (*"+receiver+") "+name+signature)
	}
	sort.Strings(lines)
	return lines
}

func renderTypeArguments(parameters *types.TypeParamList) string {
	if parameters == nil || parameters.Len() == 0 {
		return ""
	}
	values := make([]string, parameters.Len())
	for index := range values {
		values[index] = parameters.At(index).Obj().Name()
	}
	return "[" + strings.Join(values, ", ") + "]"
}

func exportedMethods(set *types.MethodSet, qualify types.Qualifier) map[string]string {
	methods := make(map[string]string)
	for index := 0; index < set.Len(); index++ {
		selection := set.At(index)
		method, ok := selection.Obj().(*types.Func)
		if !ok || !method.Exported() {
			continue
		}
		signature, ok := selection.Type().(*types.Signature)
		if !ok {
			continue
		}
		methods[method.Name()] = renderSignature(signature, qualify)
	}
	return methods
}

func renderSignature(signature *types.Signature, qualify types.Qualifier) string {
	result := renderTypeParameters(signature.TypeParams(), qualify) + renderTuple(signature.Params(), signature.Variadic(), qualify)
	switch signature.Results().Len() {
	case 0:
		return result
	case 1:
		return result + " " + types.TypeString(signature.Results().At(0).Type(), qualify)
	default:
		return result + " " + renderTuple(signature.Results(), false, qualify)
	}
}

func renderTuple(tuple *types.Tuple, variadic bool, qualify types.Qualifier) string {
	values := make([]string, tuple.Len())
	for index := range values {
		valueType := tuple.At(index).Type()
		if variadic && index == len(values)-1 {
			slice, ok := valueType.(*types.Slice)
			if ok {
				values[index] = "..." + types.TypeString(slice.Elem(), qualify)
				continue
			}
		}
		values[index] = types.TypeString(valueType, qualify)
	}
	return "(" + strings.Join(values, ", ") + ")"
}
