package vvcfg

import (
	"errors"
	"os"
	"sort"
	"strings"
)

type Origin int

const (
	OriginUnknown Origin = iota
	OriginDefault
	OriginFile
	OriginEnvironment
)

func (this Origin) String() string {
	switch this {
	case OriginDefault:
		return "default"
	case OriginFile:
		return "file"
	case OriginEnvironment:
		return "environment"
	default:
		return "unknown"
	}
}

type Field struct {
	Path        string
	Origin      Origin
	Environment string
}

type Deprecation struct {
	Path   string
	Advice string
}

type Report struct {
	Path              string
	PathOrigin        PathOrigin
	NotInspected      error
	Fields            []Field
	UnknownKeys       []string
	Deprecated        []Deprecation
	UnusedEnvironment []string
}

func (this *Report) OriginOf(path string) (Origin, bool) {
	for _, field := range this.Fields {
		if field.Path == path {
			return field.Origin, true
		}
	}
	return OriginUnknown, false
}

// String renders the report as a start-up log block. It carries where every value
// came from and never the value itself: a report that printed values would be a
// credential leak in the one line an operator always copies into a ticket.
func (this *Report) String() string {
	var text strings.Builder
	if this.Path == "" {
		text.WriteString("config: no file (" + this.PathOrigin.String() + ")")
	} else {
		text.WriteString("config: " + this.Path + " (" + this.PathOrigin.String() + ")")
	}
	if this.NotInspected != nil {
		text.WriteString("\n  ! the file was not inspected: " + this.NotInspected.Error())
	}
	for _, field := range this.Fields {
		text.WriteString("\n  " + field.Path + " <- " + field.Origin.String())
		if field.Environment != "" {
			text.WriteString(" " + field.Environment)
		}
	}
	for _, key := range this.UnknownKeys {
		text.WriteString("\n  ! unknown key: " + key)
	}
	for _, deprecation := range this.Deprecated {
		text.WriteString("\n  ! deprecated: " + deprecation.Path + " — " + deprecation.Advice)
	}
	for _, variable := range this.UnusedEnvironment {
		text.WriteString("\n  ! set and read by no field: " + variable)
	}
	return text.String()
}

type UnknownKeysError struct {
	Path string
	Keys []string
}

func (this *UnknownKeysError) Error() string {
	return "vvcfg: " + this.Path + " sets keys no field declares: " + strings.Join(this.Keys, ", ")
}

type UnusedEnvironmentError struct {
	Variables []string
}

func (this *UnusedEnvironmentError) Error() string {
	return "vvcfg: variables under a declared prefix are read by no field: " + strings.Join(this.Variables, ", ")
}

type EnvironmentSourceError struct {
	Path      string
	Variables []string
}

func (this *EnvironmentSourceError) Error() string {
	if len(this.Variables) == 0 {
		return "vvcfg: " + this.Path + " must come from the environment and declares no variable to come from"
	}
	return "vvcfg: " + this.Path + " must come from the environment (" + strings.Join(this.Variables, " or ") + ")"
}

var ErrUndeclaredPath = errors.New("vvcfg: no field declares this path")

func buildReport(declared *schema, file *document, notInspected error, path string, origin PathOrigin) *Report {
	report := &Report{Path: path, PathOrigin: origin, NotInspected: notInspected}
	present := map[string]bool{}
	if file != nil {
		present, report.UnknownKeys = file.inspect(declared)
	}
	for _, node := range declared.leaves {
		field := Field{Path: node.path, Origin: OriginDefault}
		if notInspected != nil || (file == nil && len(node.names) == 0) {
			field.Origin = OriginUnknown
		}
		if present[node.path] {
			field.Origin = OriginFile
		}
		for _, variable := range node.names {
			if _, set := os.LookupEnv(variable); set {
				field.Origin = OriginEnvironment
				field.Environment = variable
				break
			}
		}
		if node.deprecated != "" && (field.Origin == OriginFile || field.Origin == OriginEnvironment) {
			report.Deprecated = append(report.Deprecated, Deprecation{Path: node.path, Advice: node.deprecated})
		}
		report.Fields = append(report.Fields, field)
	}
	report.UnusedEnvironment = unusedEnvironment(declared)
	return report
}

func unusedEnvironment(declared *schema) []string {
	if len(declared.prefixes) == 0 {
		return nil
	}
	var unused []string
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found || declared.names[name] != nil {
			continue
		}
		if underAnyPrefix(name, declared.excluded) {
			continue
		}
		if underAnyPrefix(name, declared.prefixes) {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	return unused
}

func underAnyPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
