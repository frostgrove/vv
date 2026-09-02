package vvcfg

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

type schemaNode struct {
	path       string
	children   map[string]*schemaNode
	order      []string
	names      []string
	deprecated string
}

type schema struct {
	root      *schemaNode
	leaves    []*schemaNode
	names     map[string]*schemaNode
	prefixes  []string
	excluded  []string
	byPath    map[string]*schemaNode
	tagFormat string
}

func describe(kind reflect.Type, tagFormat string) *schema {
	built := &schema{
		root:      &schemaNode{children: map[string]*schemaNode{}},
		names:     map[string]*schemaNode{},
		byPath:    map[string]*schemaNode{},
		tagFormat: tagFormat,
	}
	built.describeStruct(dereference(kind), built.root, "", "", map[reflect.Type]bool{}, 0, false)
	return built
}

func (this *schema) describeStruct(kind reflect.Type, parent *schemaNode, path, environmentPrefix string, seen map[reflect.Type]bool, depth int, opaque bool) {
	if kind.Kind() != reflect.Struct || depth > maximumTreeDepth || seen[kind] {
		return
	}
	seen[kind] = true
	defer delete(seen, kind)

	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := this.fieldKey(field)
		if name == "-" {
			continue
		}
		fieldPrefix := environmentPrefix + field.Tag.Get("env-prefix")
		if field.Anonymous && name == "" {
			this.describeStruct(dereference(field.Type), parent, path, fieldPrefix, seen, depth+1, opaque)
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}

		node := &schemaNode{path: join(path, name), deprecated: deprecationOf(field)}
		for _, variable := range environmentNames(field) {
			node.names = append(node.names, fieldPrefix+variable)
		}
		parent.children[name] = node
		parent.order = append(parent.order, name)
		this.byPath[node.path] = node

		fieldOpaque := opaque || ownsItsEnvironment(field.Type)
		if descends(field.Type) {
			node.children = map[string]*schemaNode{}
			if fieldOpaque && fieldPrefix != "" {
				this.excludePrefix(fieldPrefix)
			}
			this.describeStruct(dereference(field.Type), node, node.path, fieldPrefix, seen, depth+1, fieldOpaque)
			if len(node.children) == 0 {
				node.children = nil
			}
		}
		if node.children == nil {
			this.leaves = append(this.leaves, node)
		}
		for _, variable := range node.names {
			this.names[variable] = node
		}
		if !fieldOpaque && fieldPrefix != "" {
			this.declarePrefix(fieldPrefix)
		}
	}
}

func (this *schema) declarePrefix(prefix string) {
	for _, declared := range this.prefixes {
		if declared == prefix {
			return
		}
	}
	this.prefixes = append(this.prefixes, prefix)
}

func (this *schema) excludePrefix(prefix string) {
	kept := this.prefixes[:0]
	for _, declared := range this.prefixes {
		if declared != prefix {
			kept = append(kept, declared)
		}
	}
	this.prefixes = kept
	this.excluded = append(this.excluded, prefix)
}

func (this *schema) fieldKey(field reflect.StructField) string {
	for _, format := range this.tagOrder() {
		tag, ok := field.Tag.Lookup(format)
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" && strings.Contains(tag, "inline") {
			return ""
		}
		if name != "" {
			return name
		}
	}
	return ""
}

func (this *schema) tagOrder() []string {
	if this.tagFormat == "" {
		return []string{"yaml", "json", "toml"}
	}
	return append([]string{this.tagFormat}, "yaml", "json", "toml")
}

func fieldName(field reflect.StructField) string {
	for _, format := range []string{"yaml", "json", "toml"} {
		tag, ok := field.Tag.Lookup(format)
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name != "" {
			return name
		}
	}
	return strings.ToLower(field.Name)
}

func environmentNames(field reflect.StructField) []string {
	tag := field.Tag.Get("env")
	if tag == "" {
		return nil
	}
	var names []string
	for _, name := range strings.Split(tag, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func deprecationOf(field reflect.StructField) string {
	tag, ok := field.Tag.Lookup("vvcfg")
	if !ok {
		return ""
	}
	for _, option := range strings.Split(tag, ",") {
		option = strings.TrimSpace(option)
		if option == "deprecated" {
			return "deprecated"
		}
		if advice, found := strings.CutPrefix(option, "deprecated="); found {
			return advice
		}
	}
	return ""
}

func dereference(kind reflect.Type) reflect.Type {
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	return kind
}

func descends(kind reflect.Type) bool {
	kind = dereference(kind)
	if kind.Kind() != reflect.Struct {
		return false
	}
	return !decodesItself(kind)
}

func decodesItself(kind reflect.Type) bool {
	pointer := reflect.PointerTo(kind)
	return pointer.Implements(textUnmarshaler) || pointer.Implements(jsonUnmarshaler) || pointer.Implements(yamlUnmarshaler)
}

func ownsItsEnvironment(kind reflect.Type) bool {
	pointer := reflect.PointerTo(dereference(kind))
	return pointer.Implements(environmentApplier) || pointer.Implements(prefixedEnvironmentApplier)
}

var (
	textUnmarshaler            = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	jsonUnmarshaler            = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	yamlUnmarshaler            = reflect.TypeOf((*yaml.Unmarshaler)(nil)).Elem()
	environmentApplier         = reflect.TypeOf((*EnvironmentApplier)(nil)).Elem()
	prefixedEnvironmentApplier = reflect.TypeOf((*PrefixedEnvironmentApplier)(nil)).Elem()
)
