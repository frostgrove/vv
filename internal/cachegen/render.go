package cachegen

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

const (
	appendBytesName   = "vvGeneratedCacheAppendBytes"
	appendStringName  = "vvGeneratedCacheAppendString"
	appendUintName    = "vvGeneratedCacheAppendUint64"
	appendBoolName    = "vvGeneratedCacheAppendBool"
	zeroPartitionName = "vvGeneratedCachePartitionIsZero"
	targetAssertName  = "vvGeneratedCacheAssertTarget"
)

type renderer struct {
	loaded       *loadedPackage
	declarations []declaration
	manifest     manifestDocument
	aliases      map[string]string
	usesMath     bool
	sequence     int
}

func render(loaded *loadedPackage, declarations []declaration, manifest manifestDocument, unconfirmed bool, previousProof string) ([]byte, error) {
	renderer := renderer{loaded: loaded, declarations: declarations, manifest: manifest}
	if err := renderer.prepareAliases(); err != nil {
		return nil, err
	}
	if err := renderer.validateGeneratedNames(); err != nil {
		return nil, err
	}
	return renderer.source(unconfirmed, previousProof)
}

func (this *renderer) source(unconfirmed bool, previousProof string) ([]byte, error) {
	var body bytes.Buffer
	fmt.Fprintf(&body, "const %s = %q\n\n", generatedMarkerName, markerValue(this.manifest.CompatibilityProof, previousProof))
	this.renderTargetAssertions(&body)
	if unconfirmed {
		body.WriteString("var VVCacheSet _vvcache.Set = \"confirm every cache scope in cache.manifest.yml\"\n")
		return this.assemble(body.String(), false)
	}
	if len(this.declarations) == 0 {
		body.WriteString("var VVCacheSet = _vvcache.MustSet()\n")
		return this.assemble(body.String(), false)
	}
	entries := make(map[string]manifestCache, len(this.manifest.Caches))
	for _, entry := range this.manifest.Caches {
		entries[entry.Name] = entry
	}
	for _, declaration := range this.declarations {
		entry := entries[declaration.logicalName]
		if err := this.renderKeyEncoder(&body, declaration, entry); err != nil {
			return nil, err
		}
		if entry.Scope.Mode == "partitioned" {
			if err := this.renderPartitioner(&body, declaration, entry); err != nil {
				return nil, err
			}
		}
	}
	this.renderAppendHelpers(&body)
	if this.hasPartitionedCache() {
		fmt.Fprintf(&body, "func %s[T comparable](value T) bool {\n", zeroPartitionName)
		body.WriteString("\tvar zero T\n\treturn value == zero\n}\n\n")
	}
	body.WriteString("var VVCacheSet = _vvcache.MustSet(\n")
	for _, declaration := range this.declarations {
		entry := entries[declaration.logicalName]
		keySyntax := this.typeSyntax(declaration, declaration.keySyntax)
		valueSyntax := this.typeSyntax(declaration, declaration.valueSyntax)
		fmt.Fprintf(&body, "\t_vvcache.MustDefine(%s, _vvcache.DefinitionSpec[%s, %s]{\n", declaration.variable, keySyntax, valueSyntax)
		fmt.Fprintf(&body, "\t\tName: %q,\n", entry.Name)
		fmt.Fprintf(&body, "\t\tNamespace: _vvcache.NamespaceTemplate{Purpose: %q, Generation: _vvcache.Generation(%d)},\n", entry.Namespace.Purpose, entry.Namespace.Generation)
		if entry.Scope.Mode == "global" {
			fmt.Fprintf(&body, "\t\tScope: _vvcache.GlobalPlan[%s](),\n", keySyntax)
		} else {
			fmt.Fprintf(&body, "\t\tScope: _vvcache.PartitionedPlan[%s](%s),\n", keySyntax, partitionerName(declaration))
		}
		fmt.Fprintf(&body, "\t\tKeys: %s,\n", this.keyCodec(declaration, entry))
		fmt.Fprintf(&body, "\t\tValues: %s,\n", this.valueCodec(declaration, entry))
		if entry.Profile.ProviderID != "" {
			fmt.Fprintf(&body, "\t\tProvider: _vvcache.ProviderID(%q),\n", entry.Profile.ProviderID)
		}
		body.WriteString("\t}),\n")
	}
	body.WriteString(")\n")
	return this.assemble(body.String(), true)
}

func (this *renderer) assemble(body string, binaryUsed bool) ([]byte, error) {
	var output bytes.Buffer
	fmt.Fprintf(&output, "package %s\n\n", this.loaded.name)
	imports := map[string]string{"_vvcache": cacheImportPath, "_vvruntime": "runtime"}
	if binaryUsed {
		imports["_vvbinary"] = "encoding/binary"
	}
	if this.usesMath {
		imports["_vvmath"] = "math"
	}
	if binaryUsed {
		for path, alias := range this.aliases {
			if path != cacheImportPath && path != "encoding/binary" && path != "math" && path != "runtime" {
				imports[alias] = path
			}
		}
	}
	lines := make([]string, 0, len(imports))
	for alias, path := range imports {
		lines = append(lines, fmt.Sprintf("%s %q", alias, path))
	}
	sort.Strings(lines)
	output.WriteString("import (\n")
	for _, line := range lines {
		fmt.Fprintf(&output, "\t%s\n", line)
	}
	output.WriteString(")\n\n")
	output.WriteString(body)
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("cachegen: generated code does not parse: %w\n%s", err, output.String())
	}
	return formatted, nil
}

func (this *renderer) prepareAliases() error {
	paths := map[string][]string{}
	for _, declaration := range this.declarations {
		for alias, path := range declaration.imports {
			paths[path] = append(paths[path], alias)
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	used := map[string]bool{"_vvcache": true, "_vvbinary": true, "_vvmath": true, "_vvruntime": true}
	for name := range this.loaded.declarations {
		used[name] = true
	}
	for _, declaration := range this.declarations {
		used[encoderName(declaration)] = true
		used[partitionerName(declaration)] = true
	}
	for _, name := range []string{generatedMarkerName, targetAssertName, "VVCacheSet", appendBytesName, appendStringName, appendUintName, appendBoolName, zeroPartitionName} {
		used[name] = true
	}
	this.aliases = map[string]string{}
	for _, path := range ordered {
		switch path {
		case cacheImportPath:
			this.aliases[path] = "_vvcache"
			continue
		case "encoding/binary":
			this.aliases[path] = "_vvbinary"
			continue
		case "math":
			this.aliases[path] = "_vvmath"
			this.usesMath = true
			continue
		case "runtime":
			this.aliases[path] = "_vvruntime"
			continue
		}
		candidates := paths[path]
		sort.Strings(candidates)
		alias := candidates[0]
		if used[alias] || alias == "_" || alias == "." {
			for index := 1; ; index++ {
				alias = fmt.Sprintf("_vvcachetype%d", index)
				if !used[alias] {
					break
				}
			}
		}
		used[alias] = true
		this.aliases[path] = alias
	}
	return nil
}

func (this *renderer) validateGeneratedNames() error {
	names := []string{generatedMarkerName, targetAssertName, "VVCacheSet"}
	if len(this.declarations) != 0 {
		names = append(names, appendBytesName, appendStringName, appendUintName, appendBoolName)
		if this.hasPartitionedCache() {
			names = append(names, zeroPartitionName)
		}
		for _, declaration := range this.declarations {
			names = append(names, encoderName(declaration))
			entry := this.entry(declaration.logicalName)
			if entry.Scope.Mode == "partitioned" {
				names = append(names, partitionerName(declaration))
			}
		}
	}
	problems := make([]string, 0)
	seen := map[string]struct{}{}
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			problems = append(problems, name+" (generated twice)")
			continue
		}
		seen[name] = struct{}{}
		if _, exists := this.loaded.declarations[name]; exists {
			problems = append(problems, name)
			continue
		}
		if _, exists := this.loaded.imports[name]; exists {
			problems = append(problems, name)
		}
	}
	for _, name := range []string{"_vvcache", "_vvbinary", "_vvmath", "_vvruntime"} {
		if _, exists := this.loaded.declarations[name]; exists {
			problems = append(problems, name)
		}
	}
	if len(problems) != 0 {
		sort.Strings(problems)
		return fmt.Errorf("cachegen: generated declarations collide with authored code: %s", strings.Join(problems, ", "))
	}
	return nil
}

func (this *renderer) renderTargetAssertions(output *bytes.Buffer) {
	fmt.Fprintf(output, "func %s() {\n", targetAssertName)
	renderTargetAssertion(output, "GOOS", this.manifest.BuildTarget.GOOS, this.manifest.BuildTarget.goosValues)
	renderTargetAssertion(output, "GOARCH", this.manifest.BuildTarget.GOARCH, this.manifest.BuildTarget.goarchValues)
	output.WriteString("}\n\n")
}

func renderTargetAssertion(output *bytes.Buffer, name, expected string, values []string) {
	fmt.Fprintf(output, "\tswitch _vvruntime.%s {\n\tcase _vvruntime.%s", name, name)
	for _, value := range values {
		if value != expected {
			fmt.Fprintf(output, ", %q", value)
		}
	}
	output.WriteString(":\n\t}\n")
}

func (this *renderer) entry(name string) manifestCache {
	for _, entry := range this.manifest.Caches {
		if entry.Name == name {
			return entry
		}
	}
	return manifestCache{}
}

func (this *renderer) typeSyntax(declaration declaration, syntax string) string {
	replacements := make(map[string]string, len(declaration.imports))
	for sourceAlias, path := range declaration.imports {
		if replacement := this.aliases[path]; replacement != sourceAlias {
			replacements[sourceAlias] = replacement
		}
	}
	return replaceQualifiers(syntax, replacements)
}

func replaceQualifiers(source string, replacements map[string]string) string {
	if len(replacements) == 0 {
		return source
	}
	tokenSet := token.NewFileSet()
	file := tokenSet.AddFile("type.go", -1, len(source))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(source), nil, scanner.ScanComments)
	type change struct {
		start       int
		end         int
		replacement string
	}
	changes := make([]change, 0)
	previousToken := token.ILLEGAL
	previousLiteral := ""
	previousOffset := 0
	for {
		position, currentToken, literal := lexer.Scan()
		if currentToken == token.PERIOD && previousToken == token.IDENT {
			if replacement, ok := replacements[previousLiteral]; ok {
				changes = append(changes, change{start: previousOffset, end: previousOffset + len(previousLiteral), replacement: replacement})
			}
		}
		if currentToken == token.EOF {
			break
		}
		previousToken = currentToken
		previousLiteral = literal
		previousOffset = file.Offset(position)
	}
	if len(changes) == 0 {
		return source
	}
	var output strings.Builder
	cursor := 0
	for _, item := range changes {
		output.WriteString(source[cursor:item.start])
		output.WriteString(item.replacement)
		cursor = item.end
	}
	output.WriteString(source[cursor:])
	return output.String()
}

func (this *renderer) keyCodec(declaration declaration, entry manifestCache) string {
	keySyntax := this.typeSyntax(declaration, declaration.keySyntax)
	return fmt.Sprintf("_vvcache.MustKeyFunc[%s](_vvcache.KeyVersion(%d), %s)", keySyntax, entry.Key.Version, encoderName(declaration))
}

func (this *renderer) valueCodec(declaration declaration, entry manifestCache) string {
	schema := fmt.Sprintf("_vvcache.ValueSchema(%d)", entry.Value.Schema)
	switch entry.Value.Codec {
	case "string":
		return "_vvcache.String(" + schema + ")"
	case "bytes":
		return "_vvcache.Bytes(" + schema + ")"
	case "time-rfc3339-utc":
		return "_vvcache.RFC3339UTC(" + schema + ")"
	default:
		return fmt.Sprintf("_vvcache.JSON[%s](%s)", this.typeSyntax(declaration, declaration.valueSyntax), schema)
	}
}

func (this *renderer) renderKeyEncoder(output *bytes.Buffer, declaration declaration, entry manifestCache) error {
	this.sequence = 0
	fmt.Fprintf(output, "func %s(key %s, limit _vvcache.KeyLimit) ([]byte, error) {\n", encoderName(declaration), this.typeSyntax(declaration, declaration.keySyntax))
	output.WriteString("\tencoded := make([]byte, 0)\n\tvar err error\n")
	if err := this.renderEncodedValue(output, declaration.keyType, "key", "\t", map[types.Type]bool{}); err != nil {
		return fmt.Errorf("cachegen: render %s key: %w", declaration.logicalName, err)
	}
	output.WriteString("\treturn encoded, nil\n}\n\n")
	_ = entry
	return nil
}

func (this *renderer) renderPartitioner(output *bytes.Buffer, declaration declaration, entry manifestCache) error {
	field := keyField(declaration.keyType, entry.Scope.PartitionField)
	if field == nil {
		return fmt.Errorf("cachegen: %s partition field is missing", declaration.logicalName)
	}
	this.sequence = 0
	fmt.Fprintf(output, "func %s(key %s, limit _vvcache.KeyLimit) ([]byte, error) {\n", partitionerName(declaration), this.typeSyntax(declaration, declaration.keySyntax))
	fmt.Fprintf(output, "\tif %s(key.%s) {\n\t\treturn nil, _vvcache.ErrInvalid\n\t}\n", zeroPartitionName, entry.Scope.PartitionField)
	output.WriteString("\tencoded := make([]byte, 0)\n\tvar err error\n")
	if err := this.renderEncodedValue(output, field.Type(), "key."+entry.Scope.PartitionField, "\t", map[types.Type]bool{}); err != nil {
		return fmt.Errorf("cachegen: render %s partition: %w", declaration.logicalName, err)
	}
	output.WriteString("\treturn encoded, nil\n}\n\n")
	return nil
}

func (this *renderer) hasPartitionedCache() bool {
	for _, entry := range this.manifest.Caches {
		if entry.Scope.Mode == "partitioned" {
			return true
		}
	}
	return false
}

func (this *renderer) renderEncodedValue(output *bytes.Buffer, value types.Type, expression, indent string, active map[types.Type]bool) error {
	value = types.Unalias(value)
	if active[value] {
		return fmt.Errorf("recursive type %s", types.TypeString(value, packagePath))
	}
	active[value] = true
	defer delete(active, value)
	switch item := value.(type) {
	case *types.Named:
		return this.renderEncodedValue(output, item.Underlying(), expression, indent, active)
	case *types.Basic:
		switch item.Kind() {
		case types.Bool:
			this.renderAppendCall(output, appendBoolName, "bool("+expression+")", indent)
		case types.String:
			this.renderAppendCall(output, appendStringName, "string("+expression+")", indent)
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
			this.renderAppendCall(output, appendUintName, "uint64(int64("+expression+"))", indent)
		case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			this.renderAppendCall(output, appendUintName, "uint64("+expression+")", indent)
		case types.Float32:
			this.usesMath = true
			this.sequence++
			name := fmt.Sprintf("float%d", this.sequence)
			fmt.Fprintf(output, "%s%s := float32(%s)\n%sif %s == 0 {\n%s\t%s = 0\n%s}\n", indent, name, expression, indent, name, indent, name, indent)
			this.renderAppendCall(output, appendUintName, "uint64(_vvmath.Float32bits("+name+"))", indent)
		case types.Float64:
			this.usesMath = true
			this.sequence++
			name := fmt.Sprintf("float%d", this.sequence)
			fmt.Fprintf(output, "%s%s := float64(%s)\n%sif %s == 0 {\n%s\t%s = 0\n%s}\n", indent, name, expression, indent, name, indent, name, indent)
			this.renderAppendCall(output, appendUintName, "_vvmath.Float64bits("+name+")", indent)
		default:
			return fmt.Errorf("unsupported basic type %s", item.Name())
		}
	case *types.Pointer:
		fmt.Fprintf(output, "%sif %s == nil {\n", indent, expression)
		this.renderAppendCall(output, appendBoolName, "false", indent+"\t")
		fmt.Fprintf(output, "%s} else {\n", indent)
		this.renderAppendCall(output, appendBoolName, "true", indent+"\t")
		if err := this.renderEncodedValue(output, item.Elem(), "(*"+expression+")", indent+"\t", active); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s}\n", indent)
	case *types.Slice:
		fmt.Fprintf(output, "%sif %s == nil {\n", indent, expression)
		this.renderAppendCall(output, appendBoolName, "false", indent+"\t")
		fmt.Fprintf(output, "%s} else {\n", indent)
		this.renderAppendCall(output, appendBoolName, "true", indent+"\t")
		if types.Identical(item.Elem(), types.Typ[types.Byte]) {
			this.renderAppendCall(output, appendBytesName, "[]byte("+expression+")", indent+"\t")
			fmt.Fprintf(output, "%s}\n", indent)
			return nil
		}
		this.renderAppendCall(output, appendUintName, "uint64(len("+expression+"))", indent+"\t")
		this.sequence++
		index := fmt.Sprintf("index%d", this.sequence)
		fmt.Fprintf(output, "%s\tfor %s := range %s {\n", indent, index, expression)
		if err := this.renderEncodedValue(output, item.Elem(), expression+"["+index+"]", indent+"\t\t", active); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s\t}\n%s}\n", indent, indent)
	case *types.Array:
		this.renderAppendCall(output, appendUintName, fmt.Sprintf("uint64(%d)", item.Len()), indent)
		this.sequence++
		index := fmt.Sprintf("index%d", this.sequence)
		fmt.Fprintf(output, "%sfor %s := range %s {\n", indent, index, expression)
		if err := this.renderEncodedValue(output, item.Elem(), expression+"["+index+"]", indent+"\t", active); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s}\n", indent)
	case *types.Struct:
		this.renderAppendCall(output, appendUintName, fmt.Sprintf("uint64(%d)", item.NumFields()), indent)
		for index := 0; index < item.NumFields(); index++ {
			field := item.Field(index)
			if !field.Exported() {
				return fmt.Errorf("field %s is unexported", field.Name())
			}
			if err := this.renderEncodedValue(output, field.Type(), expression+"."+field.Name(), indent, active); err != nil {
				return fmt.Errorf("field %s: %w", field.Name(), err)
			}
		}
	default:
		return fmt.Errorf("unsupported key type %s", types.TypeString(value, packagePath))
	}
	return nil
}

func (this *renderer) renderAppendCall(output *bytes.Buffer, function, value, indent string) {
	fmt.Fprintf(output, "%sencoded, err = %s(encoded, %s, limit.MaxBytes)\n", indent, function, value)
	fmt.Fprintf(output, "%sif err != nil {\n%s\treturn nil, err\n%s}\n", indent, indent, indent)
}

func (this *renderer) renderAppendHelpers(output *bytes.Buffer) {
	fmt.Fprintf(output, "func %s(encoded, value []byte, maximum int) ([]byte, error) {\n", appendBytesName)
	output.WriteString("\tif maximum < 0 || len(encoded) > maximum {\n\t\treturn nil, _vvcache.ErrTooLarge\n\t}\n")
	output.WriteString("\tremaining := maximum - len(encoded)\n")
	output.WriteString("\tif remaining < 4 || len(value) > remaining-4 || uint64(len(value)) > uint64(^uint32(0)) {\n\t\treturn nil, _vvcache.ErrTooLarge\n\t}\n")
	output.WriteString("\tencoded = _vvbinary.BigEndian.AppendUint32(encoded, uint32(len(value)))\n")
	output.WriteString("\treturn append(encoded, value...), nil\n}\n\n")
	fmt.Fprintf(output, "func %s(encoded []byte, value string, maximum int) ([]byte, error) {\n", appendStringName)
	output.WriteString("\tif maximum < 0 || len(encoded) > maximum {\n\t\treturn nil, _vvcache.ErrTooLarge\n\t}\n")
	output.WriteString("\tremaining := maximum - len(encoded)\n")
	output.WriteString("\tif remaining < 4 || len(value) > remaining-4 || uint64(len(value)) > uint64(^uint32(0)) {\n\t\treturn nil, _vvcache.ErrTooLarge\n\t}\n")
	output.WriteString("\tencoded = _vvbinary.BigEndian.AppendUint32(encoded, uint32(len(value)))\n")
	output.WriteString("\treturn append(encoded, value...), nil\n}\n\n")
	fmt.Fprintf(output, "func %s(encoded []byte, value uint64, maximum int) ([]byte, error) {\n", appendUintName)
	output.WriteString("\tif maximum < 0 || maximum-len(encoded) < 8 {\n\t\treturn nil, _vvcache.ErrTooLarge\n\t}\n")
	output.WriteString("\treturn _vvbinary.BigEndian.AppendUint64(encoded, value), nil\n}\n\n")
	fmt.Fprintf(output, "func %s(encoded []byte, value bool, maximum int) ([]byte, error) {\n", appendBoolName)
	output.WriteString("\tif maximum < 0 || maximum-len(encoded) < 1 {\n\t\treturn nil, _vvcache.ErrTooLarge\n\t}\n")
	output.WriteString("\tif value {\n\t\treturn append(encoded, 1), nil\n\t}\n")
	output.WriteString("\treturn append(encoded, 0), nil\n}\n\n")
}

func encoderName(declaration declaration) string {
	return "vvGeneratedCacheKey_" + declarationHash(declaration)
}

func partitionerName(declaration declaration) string {
	return "vvGeneratedCachePartition_" + declarationHash(declaration)
}

func declarationHash(declaration declaration) string {
	digest := sha256.Sum256([]byte(declaration.logicalName))
	return fmt.Sprintf("%x", digest[:6])
}
