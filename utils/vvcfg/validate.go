package vvcfg

import (
	"errors"
	"reflect"
	"sort"
	"strconv"
)

type Validator interface {
	Validate() error
}

type SelfValidator interface {
	ValidateSelf() error
}

type CrossValidator interface {
	ValidateCross() error
}

type ValidationError struct {
	Path  string
	Cause error
}

func (this *ValidationError) Error() string {
	if this.Path == "" {
		return this.Cause.Error()
	}
	return this.Path + ": " + this.Cause.Error()
}

func (this *ValidationError) Unwrap() error { return this.Cause }

const maximumTreeDepth = 64

type nodeKey struct {
	kind    reflect.Type
	address uintptr
}

type treeWalker struct {
	visited  map[nodeKey]bool
	failures []error
	crossed  []crossNode
}

type crossNode struct {
	path   string
	target CrossValidator
}

// ValidateTree asks every node that can validate itself to do so, exactly once per
// node, before any node is asked for its cross-field rules. Self failures are joined
// rather than raced: an operator whose file has two broken blocks learns about both.
// Cross rules run only over a tree whose nodes are individually valid, so a rule that
// reads two blocks may assume each block holds.
//
// A node spelling the older Validate owns its subtree and the walk stops there: that
// method is as often a hand-written forwarder as a rule, and a block that merges a
// fragment with its parent before checking it — a database replica is the live case —
// refuses the fragment when asked directly. ValidateSelf is the promise that a method
// is about this node alone, and the walk continues past it.
//
// Recursion is bounded twice: by the address of every pointer and map already seen,
// which is what makes a self-referential configuration terminate, and by depth.
func ValidateTree(root any) error {
	walker := &treeWalker{visited: make(map[nodeKey]bool)}
	walker.walk(reflect.ValueOf(root), "", 0)
	if len(walker.failures) > 0 {
		return errors.Join(walker.failures...)
	}
	var failures []error
	for _, node := range walker.crossed {
		if err := node.target.ValidateCross(); err != nil {
			failures = append(failures, &ValidationError{Path: node.path, Cause: err})
		}
	}
	return errors.Join(failures...)
}

func (this *treeWalker) walk(value reflect.Value, path string, depth int) {
	if !value.IsValid() || depth > maximumTreeDepth {
		return
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return
		}
		if value.Kind() == reflect.Pointer && this.seen(nodeKey{kind: value.Type(), address: value.Pointer()}) {
			return
		}
		this.walk(value.Elem(), path, depth+1)
	case reflect.Struct:
		descend := true
		if value.CanAddr() && value.Addr().CanInterface() {
			descend = this.validate(value.Addr().Interface(), path)
		} else if value.CanInterface() {
			descend = this.validate(value.Interface(), path)
		}
		if descend {
			this.walkFields(value, path, depth)
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return
		}
		for index := 0; index < value.Len(); index++ {
			this.walk(value.Index(index), path+"["+strconv.Itoa(index)+"]", depth+1)
		}
	case reflect.Map:
		if value.IsNil() || this.seen(nodeKey{kind: value.Type(), address: value.Pointer()}) {
			return
		}
		keys := value.MapKeys()
		sort.Slice(keys, func(left, right int) bool {
			return keyText(keys[left]) < keyText(keys[right])
		})
		for _, key := range keys {
			this.walk(value.MapIndex(key), path+"["+keyText(key)+"]", depth+1)
		}
	}
}

func (this *treeWalker) walkFields(value reflect.Value, path string, depth int) {
	structure := value.Type()
	for index := 0; index < structure.NumField(); index++ {
		field := structure.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := fieldName(field)
		if name == "-" {
			continue
		}
		this.walk(value.Field(index), join(path, name), depth+1)
	}
}

func (this *treeWalker) seen(key nodeKey) bool {
	if this.visited[key] {
		return true
	}
	this.visited[key] = true
	return false
}

func (this *treeWalker) validate(target any, path string) (descend bool) {
	descend = true
	switch validator := target.(type) {
	case SelfValidator:
		if err := validator.ValidateSelf(); err != nil {
			this.failures = append(this.failures, &ValidationError{Path: path, Cause: err})
		}
	case Validator:
		descend = false
		if err := validator.Validate(); err != nil {
			this.failures = append(this.failures, &ValidationError{Path: path, Cause: err})
		}
	}
	if validator, ok := target.(CrossValidator); ok {
		this.crossed = append(this.crossed, crossNode{path: path, target: validator})
	}
	return descend
}

func keyText(key reflect.Value) string {
	if key.Kind() == reflect.String {
		return key.String()
	}
	return valueText(key)
}

func valueText(value reflect.Value) string {
	if value.CanInterface() {
		if text, ok := value.Interface().(interface{ String() string }); ok {
			return text.String()
		}
	}
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	default:
		return "?"
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
