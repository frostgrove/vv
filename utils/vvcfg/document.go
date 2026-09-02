package vvcfg

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

var ErrUnreadableFormat = errors.New("vvcfg: this file format cannot be inspected: strict reading and the provenance report need .yaml, .yml, .json, .toml or .env")

type document struct {
	format    string
	values    map[string]any
	variables []string
}

func readDocument(path string) (*document, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."); format {
	case "yaml", "yml":
		values := map[string]any{}
		if err := yaml.Unmarshal(body, &values); err != nil {
			return nil, hideDecodeCause("vvcfg: inspecting "+path, err)
		}
		return &document{format: "yaml", values: values}, nil
	case "json":
		values := map[string]any{}
		if err := json.Unmarshal(body, &values); err != nil {
			return nil, hideDecodeCause("vvcfg: inspecting "+path, err)
		}
		return &document{format: "json", values: values}, nil
	case "toml":
		values := map[string]any{}
		if err := toml.Unmarshal(body, &values); err != nil {
			return nil, hideDecodeCause("vvcfg: inspecting "+path, err)
		}
		return &document{format: "toml", values: values}, nil
	case "env":
		return &document{format: "env", variables: environmentFileKeys(string(body))}, nil
	default:
		return nil, ErrUnreadableFormat
	}
}

func environmentFileKeys(body string) []string {
	var keys []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			keys = append(keys, name)
		}
	}
	return keys
}

func (this *document) inspect(declared *schema) (present map[string]bool, unknown []string) {
	present = map[string]bool{}
	if this.format == "env" {
		for _, variable := range this.variables {
			if node, ok := declared.names[variable]; ok {
				present[node.path] = true
			} else {
				unknown = append(unknown, variable)
			}
		}
		sort.Strings(unknown)
		return present, unknown
	}
	walkDocument(this.values, declared.root, "", present, &unknown)
	sort.Strings(unknown)
	return present, unknown
}

func walkDocument(values map[string]any, node *schemaNode, path string, present map[string]bool, unknown *[]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		child := lookup(node, key)
		if child == nil {
			*unknown = append(*unknown, join(path, key))
			continue
		}
		present[child.path] = true
		if child.children == nil {
			continue
		}
		nested, ok := values[key].(map[string]any)
		if !ok {
			continue
		}
		walkDocument(nested, child, child.path, present, unknown)
	}
}

func lookup(node *schemaNode, key string) *schemaNode {
	if child, ok := node.children[key]; ok {
		return child
	}
	for name, child := range node.children {
		if strings.EqualFold(name, key) {
			return child
		}
	}
	return nil
}
