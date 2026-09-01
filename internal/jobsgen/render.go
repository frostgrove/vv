package jobsgen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"sort"
	"strconv"
)

func render(loaded *loadedPackage, declarations []declaration, document manifestDocument) ([]byte, error) {
	entries := make(map[string]manifestJob, len(document.Jobs))
	for _, entry := range document.Jobs {
		entries[entry.Variable] = entry
	}
	paths := payloadImportPaths(loaded, declarations)
	aliases := make(map[string]string, len(paths)+1)
	aliases[jobsImportPath] = "_vvjobs"
	index := 0
	for _, path := range paths {
		if path == jobsImportPath {
			continue
		}
		aliases[path] = fmt.Sprintf("_vvjobstype%d", index)
		index++
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "package %s\n\n", loaded.name)
	output.WriteString("import (\n")
	fmt.Fprintf(&output, "\t_vvjobs %q\n", jobsImportPath)
	for _, path := range paths {
		if path != jobsImportPath {
			fmt.Fprintf(&output, "\t%s %q\n", aliases[path], path)
		}
	}
	output.WriteString(")\n\n")
	fmt.Fprintf(&output, "const _vvGeneratedJobsArtifact = %q\n\n", generatedMarker)
	output.WriteString("func _vvJobsMustName(value string) _vvjobs.Name {\n")
	output.WriteString("\tname, err := _vvjobs.ParseName(value)\n")
	output.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	output.WriteString("\treturn name\n}\n\n")
	output.WriteString("var VVJobsCatalog = func() _vvjobs.Catalog {\n")
	for _, declaration := range declarations {
		entry := entries[declaration.variable]
		payload := types.TypeString(declaration.payload, func(pkg *types.Package) string {
			if pkg == nil || pkg.Path() == loaded.types.Path() {
				return ""
			}
			return aliases[pkg.Path()]
		})
		partition := "_vvjobs.PartitionGlobal"
		if entry.Partition == "tenant_required" {
			partition = "_vvjobs.PartitionTenantRequired"
		}
		fmt.Fprintf(&output, "\t_vvjobs.MustMaterialize(%s, _vvjobs.GeneratedDefinitionSpec[%s]{\n", declaration.variable, payload)
		fmt.Fprintf(&output, "\t\tName: _vvJobsMustName(%s),\n", strconv.Quote(entry.Name))
		fmt.Fprintf(&output, "\t\tCodec: _vvjobs.JSON[%s](_vvjobs.SchemaVersion(%d)),\n", payload, entry.Codec.Version)
		fmt.Fprintf(&output, "\t\tPartition: %s,\n", partition)
		output.WriteString("\t})\n")
	}
	output.WriteString("\treturn _vvjobs.MustCatalog(")
	for index, declaration := range declarations {
		if index != 0 {
			output.WriteString(", ")
		}
		output.WriteString(declaration.variable)
	}
	output.WriteString(")\n}()\n")
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("jobsgen: format generated Go: %w", err)
	}
	return formatted, nil
}

func payloadImportPaths(loaded *loadedPackage, declarations []declaration) []string {
	paths := map[string]struct{}{}
	for _, declaration := range declarations {
		_ = types.TypeString(declaration.payload, func(pkg *types.Package) string {
			if pkg != nil && pkg.Path() != loaded.types.Path() {
				paths[pkg.Path()] = struct{}{}
			}
			return pkg.Name()
		})
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
