package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type SourceShape struct {
	Availability string                 `json:"availability"`
	Evolution    string                 `json:"evolution,omitempty"`
	Components   []string               `json:"components"`
	Kind         string                 `json:"kind"`
	File         string                 `json:"file"`
	Type         string                 `json:"type"`
	Scope        string                 `json:"scope"`
	Members      map[string]ShapeMember `json:"members"`
}
type ShapeMember struct {
	Type       string   `json:"type"`
	Signals    []string `json:"signals,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
	Gates      []string `json:"gates,omitempty"`
	Nested     string   `json:"nested,omitempty"`
	Excluded   string   `json:"excluded,omitempty"`
}

func validateShapeMappings(reg Registry) error {
	if len(reg.SourceShapes) == 0 {
		return errors.New("structured source inventories are required")
	}
	for key, shape := range reg.SourceShapes {
		if !validToken(key) || !oneOf(shape.Availability, "current", "planned") || !oneOf(shape.Kind, "fields", "accessors", "interface", "functions", "callback") || !token.IsIdentifier(shape.Type) || shape.Scope == "" || len(shape.Members) == 0 {
			return fmt.Errorf("invalid source shape %s", key)
		}
		if shape.Availability == "current" && shape.Evolution != "" || shape.Availability == "planned" && !oneOf(shape.Evolution, "new", "additive") {
			return fmt.Errorf("source shape %s has invalid evolution", key)
		}
		if err := repositoryFile(shape.File); err != nil {
			return err
		}
		if len(shape.Components) == 0 {
			return fmt.Errorf("source shape %s has no owning component", key)
		}
		owners := map[string]bool{}
		for _, component := range shape.Components {
			if _, ok := reg.Components[component]; !ok || owners[component] {
				return fmt.Errorf("source shape %s has invalid component ownership", key)
			}
			owners[component] = true
		}
		for name, member := range shape.Members {
			mapped := len(member.Signals) > 0 || len(member.Attributes) > 0 || member.Nested != ""
			if !ast.IsExported(name) || member.Type == "" || mapped == (strings.TrimSpace(member.Excluded) != "") {
				return fmt.Errorf("source member %s.%s must be mapped or explicitly excluded", key, name)
			}
			seen := map[string]bool{}
			for _, id := range member.Signals {
				if _, ok := reg.Signals[id]; !ok || seen[id] {
					return fmt.Errorf("source member %s.%s has invalid signal target", key, name)
				}
				seen[id] = true
				if !contains(reg.Signals[id].Inputs, key+"."+name) {
					return fmt.Errorf("source member %s.%s has a nonreciprocal signal mapping", key, name)
				}
				if !signalUsesComponent(reg, reg.Signals[id], shape.Components) {
					return fmt.Errorf("source member %s.%s maps to a foreign component signal %s", key, name, id)
				}
			}
			seen = map[string]bool{}
			for _, id := range member.Attributes {
				if _, ok := reg.Attributes[id]; !ok || seen[id] {
					return fmt.Errorf("source member %s.%s has invalid attribute target", key, name)
				}
				seen[id] = true
				consumed := false
				for _, signal := range member.Signals {
					for _, variant := range reg.Signals[signal].Variants {
						_, direct := variant.Attributes[id]
						_, base := reg.AttributeSets[variant.Base][id]
						consumed = consumed || direct || base
					}
				}
				if !consumed {
					return fmt.Errorf("source member %s.%s attribute %s is not consumed by its signals", key, name, id)
				}
			}
			seen = map[string]bool{}
			for _, signal := range member.Gates {
				if seen[signal] || !contains(member.Signals, signal) || !memberOutputIdentifiers(member.Type)["bool"] {
					return fmt.Errorf("source member %s.%s has invalid gate target %s", key, name, signal)
				}
				seen[signal] = true
			}
			if member.Nested != "" {
				target, ok := reg.SourceShapes[member.Nested]
				if !ok {
					return fmt.Errorf("source member %s.%s has unknown nested shape", key, name)
				}
				if !memberOutputIdentifiers(member.Type)[target.Type] {
					return fmt.Errorf("source member %s.%s does not return nested shape %s", key, name, member.Nested)
				}
				for _, component := range shape.Components {
					if !contains(target.Components, component) {
						return fmt.Errorf("source member %s.%s crosses component ownership through %s", key, name, member.Nested)
					}
				}
			}
		}
	}
	if err := validateNestedShapeGraph(reg); err != nil {
		return err
	}
	for key, signal := range reg.Signals {
		if (signal.ValueSource != "") == (signal.ComputedSource != "") {
			return fmt.Errorf("signal %s requires one direct or computed value source", key)
		}
		if signal.ComputedSource != "" && !oneOf(signal.ComputedSource, "logical_call", "elapsed", "event_count", "event_measurement", "aggregate", "classified_result", "stream", "carrier", "handler") {
			return fmt.Errorf("signal %s has an unknown computed source", key)
		}
		if signal.ValueSource != "" && !contains(signal.Inputs, signal.ValueSource) {
			return fmt.Errorf("signal %s direct value is not an input", key)
		}
		if signal.ValueSource != "" {
			member, _ := shapeMember(reg, signal.ValueSource)
			if signal.Kind != "metric" || !oneOf(member.Type, "int", "int64", "float64", "time.Duration", "func() int", "func() int64", "func() float64", "func() time.Duration") {
				return fmt.Errorf("signal %s direct measurement is not numeric", key)
			}
		}
		if len(signal.Inputs) == 0 {
			return fmt.Errorf("signal %s has no source inputs", key)
		}
		seen := map[string]bool{}
		for _, reference := range signal.Inputs {
			member, ok := shapeMember(reg, reference)
			if !ok || seen[reference] || !contains(member.Signals, key) || member.Excluded != "" {
				return fmt.Errorf("signal %s has an invalid or nonreciprocal source input %s", key, reference)
			}
			seen[reference] = true
			shapeKey, _, _ := strings.Cut(reference, ".")
			if signal.Availability == "implemented" && reg.SourceShapes[shapeKey].Availability != "current" {
				return fmt.Errorf("implemented signal %s depends on planned source %s", key, shapeKey)
			}
		}
		if err := validateNestedInputReachability(reg, key, signal); err != nil {
			return err
		}
		if err := validateComputedProjection(reg, key, signal); err != nil {
			return err
		}
		if err := validateSignalProjection(reg, key, signal); err != nil {
			return err
		}
		if err := validateVariantDisjointness(reg, key, signal); err != nil {
			return err
		}
	}
	return nil
}

func validateNestedShapeGraph(reg Registry) error {
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return fmt.Errorf("source shape %s participates in a nested cycle", key)
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, member := range reg.SourceShapes[key].Members {
			if member.Nested != "" {
				if err := visit(member.Nested); err != nil {
					return err
				}
			}
		}
		delete(visiting, key)
		visited[key] = true
		return nil
	}
	for key := range reg.SourceShapes {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

func validateNestedInputReachability(reg Registry, signalKey string, signal Signal) error {
	parents := map[string]bool{}
	for _, shape := range reg.SourceShapes {
		for _, member := range shape.Members {
			if member.Nested != "" {
				parents[member.Nested] = true
			}
		}
	}
	reachable := map[string]bool{}
	queue := []string{}
	mark := func(key string) {
		if !reachable[key] && signalUsesComponent(reg, signal, reg.SourceShapes[key].Components) {
			reachable[key] = true
			queue = append(queue, key)
		}
	}
	for _, reference := range signal.Inputs {
		shapeKey, memberName, _ := strings.Cut(reference, ".")
		member := reg.SourceShapes[shapeKey].Members[memberName]
		shape := reg.SourceShapes[shapeKey]
		if !parents[shapeKey] || shape.Kind == "interface" || shape.Kind == "functions" || shape.Kind == "callback" {
			mark(shapeKey)
		}
		for identifier := range memberOutputIdentifiers(member.Type) {
			for target, shape := range reg.SourceShapes {
				if shape.Type == identifier {
					mark(target)
				}
			}
		}
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, member := range reg.SourceShapes[key].Members {
			if member.Nested != "" {
				mark(member.Nested)
			}
		}
	}
	for _, reference := range signal.Inputs {
		shapeKey, _, _ := strings.Cut(reference, ".")
		if !reachable[shapeKey] {
			return fmt.Errorf("signal %s input %s is not reachable through its source shape graph", signalKey, reference)
		}
	}
	return nil
}

type signalProjectionSources struct {
	attributes      map[string]bool
	attributeInputs map[string]map[string]bool
	operationValues map[string]bool
	result          bool
}

func validateComputedProjection(reg Registry, signalKey string, signal Signal) error {
	if signal.ValueSource != "" {
		for _, reference := range signal.Inputs {
			shapeKey, _, _ := strings.Cut(reference, ".")
			shape := reg.SourceShapes[shapeKey]
			member, _ := shapeMember(reg, reference)
			if oneOf(shape.Kind, "interface", "functions", "callback") && memberCallable(member.Type) {
				return nil
			}
		}
		return fmt.Errorf("signal %s direct measurement has no callable observation boundary", signalKey)
	}
	callable := false
	numeric := false
	boolean := false
	textual := false
	valueAccessor := false
	gate := false
	for _, reference := range signal.Inputs {
		member, _ := shapeMember(reg, reference)
		identifiers := memberOutputIdentifiers(member.Type)
		callable = callable || memberCallable(member.Type)
		numeric = numeric || identifiers["int"] || identifiers["int64"] || identifiers["float64"] || identifiers["Duration"]
		boolean = boolean || identifiers["bool"]
		textual = textual || identifiers["string"]
		valueAccessor = valueAccessor || memberCallable(member.Type) && len(identifiers) > 0 && !identifiers["error"]
		gate = gate || contains(member.Gates, signalKey)
	}
	valid := false
	switch signal.ComputedSource {
	case "logical_call", "event_count":
		valid = callable
	case "elapsed":
		valid = callable || numeric
	case "event_measurement", "classified_result":
		valid = callable && numeric
	case "handler":
		valid = callable && (numeric || valueAccessor)
	case "aggregate":
		valid = callable && (numeric || boolean) && (!numeric || gate)
	case "stream":
		valid = callable && numeric
	case "carrier":
		valid = callable && textual
	}
	if !valid {
		return fmt.Errorf("signal %s has incomplete %s measurement provenance", signalKey, signal.ComputedSource)
	}
	return nil
}

func validateSignalProjection(reg Registry, signalKey string, signal Signal) error {
	sources := signalProjectionSources{attributes: map[string]bool{}, attributeInputs: map[string]map[string]bool{}, operationValues: map[string]bool{}}
	for _, reference := range signal.Inputs {
		shapeKey, memberName, _ := strings.Cut(reference, ".")
		shape := reg.SourceShapes[shapeKey]
		member := shape.Members[memberName]
		for _, attribute := range member.Attributes {
			sources.attributes[attribute] = true
			if sources.attributeInputs[attribute] == nil {
				sources.attributeInputs[attribute] = map[string]bool{}
			}
			sources.attributeInputs[attribute][reference] = true
		}
		if memberResultClassifies(member.Type) {
			sources.result = true
		}
		if shape.Kind == "interface" || shape.Kind == "functions" {
			for _, component := range shape.Components {
				if value, ok := operationValue(reg.Components[component].Operations, memberName); ok {
					sources.operationValues[value] = true
				}
			}
		}
	}
	for attribute, states := range signalAttributeStates(reg, signal) {
		metadata := reg.Attributes[attribute]
		if states[projectionDeclared] {
			if err := validateDeclaredSourceSet(signalKey, attribute, signal.DeclaredSources[attribute], sources.attributeInputs[attribute]); err != nil {
				return err
			}
		}
		if metadata.Owner == "identity" {
			continue
		}
		if states[projectionDeclared] && !sources.attributes[attribute] {
			return fmt.Errorf("signal %s declared attribute %s has no source projection", signalKey, attribute)
		}
		if len(states) < 2 && !signalAttributeNarrowsDomain(reg, signal, attribute) {
			continue
		}
		if sources.attributes[attribute] {
			continue
		}
		switch {
		case metadata.Owner == "component" && metadata.Vocabulary == "operations" && operationStatesCovered(states, sources.operationValues):
			continue
		case metadata.Owner == "component" && (metadata.Vocabulary == "outcomes" || metadata.Vocabulary == "error_code") && sources.result:
			continue
		case metadata.Owner == "global" && sources.result:
			continue
		}
		return fmt.Errorf("signal %s varying attribute %s has no source projection", signalKey, attribute)
	}
	states := signalAttributeStates(reg, signal)
	for attribute := range signal.DeclaredSources {
		if !states[attribute][projectionDeclared] {
			return fmt.Errorf("signal %s declares sources for non-declared attribute %s", signalKey, attribute)
		}
	}
	statuses := map[string]bool{}
	for _, variant := range signal.Variants {
		statuses[variant.Status] = true
	}
	if len(statuses) > 1 && !sources.result {
		return fmt.Errorf("signal %s status selection has no result projection", signalKey)
	}
	return nil
}

func validateDeclaredSourceSet(signal, attribute string, expected []string, actual map[string]bool) error {
	if len(expected) == 0 || len(expected) != len(actual) {
		return fmt.Errorf("signal %s declared attribute %s has an incomplete source set", signal, attribute)
	}
	seen := map[string]bool{}
	for _, reference := range expected {
		if seen[reference] || !actual[reference] {
			return fmt.Errorf("signal %s declared attribute %s has invalid source %s", signal, attribute, reference)
		}
		seen[reference] = true
	}
	return nil
}

func signalAttributeNarrowsDomain(reg Registry, signal Signal, attribute string) bool {
	for _, variant := range signal.Variants {
		binding, ok := variant.Attributes[attribute]
		if !ok {
			binding, ok = reg.AttributeSets[variant.Base][attribute]
		}
		if ok && !binding.Declared && len(reg.Domains[binding.Domain].Values) > 1 {
			return true
		}
	}
	return false
}

func validateVariantDisjointness(reg Registry, signalKey string, signal Signal) error {
	variants := make([]WireVariant, len(signal.Variants))
	for index, variant := range signal.Variants {
		resolved, err := resolveVariant(reg, variant, signal.Kind == "metric")
		if err != nil {
			return err
		}
		variants[index] = resolved
	}
	for right := 1; right < len(variants); right++ {
		for left := 0; left < right; left++ {
			if wireVariantsOverlap(reg, variants[left], variants[right]) {
				return fmt.Errorf("signal %s variants %d and %d admit the same projection", signalKey, left, right)
			}
		}
	}
	return nil
}

func wireVariantsOverlap(reg Registry, left, right WireVariant) bool {
	if left.Status != right.Status || !predicatesOverlap(left.When, right.When) {
		return false
	}
	keys := map[string]bool{}
	for key := range left.Attributes {
		keys[key] = true
	}
	for key := range right.Attributes {
		keys[key] = true
	}
	for key := range keys {
		first, firstOK := left.Attributes[key]
		second, secondOK := right.Attributes[key]
		if !firstOK {
			if !second.Optional {
				return false
			}
			continue
		}
		if !secondOK {
			if !first.Optional {
				return false
			}
			continue
		}
		if first.Optional && second.Optional {
			continue
		}
		if !attributeDomainsOverlap(reg, key, first, second) {
			return false
		}
	}
	return true
}

func predicatesOverlap(left, right []SourcePredicate) bool {
	type constraint struct {
		equal    *int64
		minimum  int64
		hasFloor bool
	}
	constraints := map[string]constraint{}
	for _, predicate := range append(append([]SourcePredicate(nil), left...), right...) {
		current := constraints[predicate.Fact]
		if predicate.Operator == "eq" {
			if current.equal != nil && *current.equal != predicate.Value || current.hasFloor && predicate.Value <= current.minimum {
				return false
			}
			value := predicate.Value
			current.equal = &value
		} else {
			if current.equal != nil && *current.equal <= predicate.Value {
				return false
			}
			if !current.hasFloor || predicate.Value > current.minimum {
				current.minimum = predicate.Value
				current.hasFloor = true
			}
		}
		constraints[predicate.Fact] = current
	}
	return true
}

func attributeDomainsOverlap(reg Registry, wireName string, left, right WireAttribute) bool {
	if left.Declared && right.Declared {
		return true
	}
	if !left.Declared && !right.Declared {
		for _, first := range left.Values {
			if contains(right.Values, first) {
				return true
			}
		}
		return false
	}
	declared, finite := left, right
	if right.Declared {
		declared, finite = right, left
	}
	metadata := Attribute{}
	for _, attribute := range reg.Attributes {
		if attribute.Name == wireName {
			metadata = attribute
			break
		}
	}
	for _, value := range finite.Values {
		if len(value) > 0 && len(value) <= declared.MaxBytes && len(value) <= metadata.MaxBytes {
			valid := true
			for _, char := range value {
				if !validDeclaredRune(char) {
					valid = false
					break
				}
			}
			if valid {
				return true
			}
		}
	}
	return false
}

func validDeclaredRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || value == '.' || value == '_' || value == '-'
}

const (
	projectionAbsent   = "\x00absent"
	projectionDeclared = "\x00declared"
)

func signalAttributeStates(reg Registry, signal Signal) map[string]map[string]bool {
	attributes := map[string]bool{}
	variants := make([]map[string]Binding, len(signal.Variants))
	for index, variant := range signal.Variants {
		bindings := map[string]Binding{}
		for key, binding := range reg.AttributeSets[variant.Base] {
			bindings[key] = binding
			attributes[key] = true
		}
		for key, binding := range variant.Attributes {
			bindings[key] = binding
			attributes[key] = true
		}
		variants[index] = bindings
	}
	result := map[string]map[string]bool{}
	for attribute := range attributes {
		states := map[string]bool{}
		for _, bindings := range variants {
			binding, ok := bindings[attribute]
			if !ok {
				states[projectionAbsent] = true
				continue
			}
			if binding.Declared {
				states[projectionDeclared] = true
			} else {
				values := reg.Domains[binding.Domain].Values
				if len(binding.Values) > 0 {
					values = binding.Values
				}
				if binding.Const != "" {
					values = []string{binding.Const}
				}
				for _, value := range values {
					states[value] = true
				}
			}
			if binding.Optional {
				states[projectionAbsent] = true
			}
		}
		result[attribute] = states
	}
	return result
}

func operationValue(vocabulary Vocabulary, member string) (string, bool) {
	value := ""
	for _, source := range vocabulary.Source.Members {
		if source.Symbol == member {
			value = source.Value
			break
		}
	}
	for _, mapping := range vocabulary.Mapping {
		if mapping.From == value && value != "" {
			return mapping.To, true
		}
	}
	return "", false
}

func operationStatesCovered(states, values map[string]bool) bool {
	for state := range states {
		if state != projectionAbsent && !values[state] {
			return false
		}
	}
	return !states[projectionAbsent]
}

func memberResultClassifies(memberType string) bool {
	identifiers := memberOutputIdentifiers(memberType)
	return identifiers["error"]
}

func memberCallable(memberType string) bool {
	return strings.HasPrefix(strings.TrimSpace(memberType), "func")
}

func memberOutputIdentifiers(memberType string) map[string]bool {
	text := strings.TrimSpace(memberType)
	if memberCallable(text) {
		opening := strings.IndexByte(text, '(')
		if bracket := strings.IndexByte(text, '['); bracket >= 0 && bracket < opening {
			depth := 0
			for index := bracket; index < len(text); index++ {
				switch text[index] {
				case '[':
					depth++
				case ']':
					depth--
					if depth == 0 {
						opening = strings.IndexByte(text[index+1:], '(')
						if opening >= 0 {
							opening += index + 1
						}
						index = len(text)
					}
				}
			}
		}
		if opening < 0 {
			return map[string]bool{}
		}
		depth := 0
		closing := -1
		for index := opening; index < len(text); index++ {
			switch text[index] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					closing = index
					index = len(text)
				}
			}
		}
		if closing < 0 {
			return map[string]bool{}
		}
		text = text[closing+1:]
	}
	result := map[string]bool{}
	set := token.NewFileSet()
	file := set.AddFile("member", -1, len(text))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(text), nil, 0)
	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind == token.IDENT {
			result[literal] = true
		}
	}
	return result
}

func validateSourceShapes(reg Registry, root string) error {
	if err := validateShapeMappings(reg); err != nil {
		return err
	}
	for key, shape := range reg.SourceShapes {
		if shape.Availability == "planned" {
			if err := validatePlannedSourceShape(key, shape, reg, root); err != nil {
				return fmt.Errorf("source shape %s: %w", key, err)
			}
			continue
		}
		if err := validateSourceShape(shape, root); err != nil {
			return fmt.Errorf("source shape %s: %w", key, err)
		}
	}
	return nil
}

func validatePlannedSourceShape(key string, shape SourceShape, reg Registry, root string) error {
	if shape.Availability != "planned" || !oneOf(shape.Evolution, "new", "additive") {
		return errors.New("planned source requires an explicit new or additive evolution mode")
	}
	actual, err := sourceShapeMembers(shape, root)
	if shape.Evolution == "new" {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errSourceTypeAbsent) {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.New("planned source already exists; promote and reconcile it")
	}
	if err != nil {
		return err
	}
	companion := false
	for otherKey, other := range reg.SourceShapes {
		if otherKey == key || other.Availability != "current" || other.File != shape.File || other.Type != shape.Type || other.Kind != shape.Kind {
			continue
		}
		companion = true
		for name := range shape.Members {
			if _, exists := other.Members[name]; exists {
				return fmt.Errorf("planned additive member %s overlaps current source shape %s", name, otherKey)
			}
		}
	}
	if !companion {
		return errors.New("planned additive source has no current companion")
	}
	for name := range shape.Members {
		if _, exists := actual[name]; exists {
			return fmt.Errorf("planned additive member %s already exists; promote and reconcile it", name)
		}
	}
	return nil
}

func signalUsesComponent(reg Registry, signal Signal, owners []string) bool {
	for _, variant := range signal.Variants {
		bindings := map[string]Binding{}
		for key, value := range reg.AttributeSets[variant.Base] {
			bindings[key] = value
		}
		for key, value := range variant.Attributes {
			bindings[key] = value
		}
		for key, value := range bindings {
			if reg.Attributes[key].Owner != "identity" {
				continue
			}
			for _, owner := range owners {
				if reg.Components[owner].WireValue == value.Const {
					return true
				}
			}
		}
	}
	return false
}

func validateSourceShape(shape SourceShape, root string) error {
	actual, err := sourceShapeMembers(shape, root)
	if err != nil {
		return err
	}
	if len(actual) != len(shape.Members) {
		return fmt.Errorf("source has %d members, registry has %d", len(actual), len(shape.Members))
	}
	for name, member := range shape.Members {
		if actual[name] != member.Type {
			return fmt.Errorf("member %s source type %q differs from registry %q", name, actual[name], member.Type)
		}
	}
	return nil
}

func sourceShapeMembers(shape SourceShape, root string) (map[string]string, error) {
	if err := repositoryFile(shape.File); err != nil {
		return nil, err
	}
	path := filepath.Join(root, shape.File)
	if shape.Kind == "interface" {
		return (interfaceResolver{root: root}).methods(path, shape.Type, map[string]bool{})
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	if shape.Kind == "functions" {
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && ast.IsExported(function.Name.Name) {
				result[function.Name.Name] = sourceType(function.Type)
			}
		}
		return result, nil
	}
	found := false
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}
		for _, raw := range group.Specs {
			spec := raw.(*ast.TypeSpec)
			if spec.Name.Name != shape.Type {
				continue
			}
			found = true
			if shape.Kind == "callback" {
				body, ok := spec.Type.(*ast.FuncType)
				if !ok {
					return nil, errors.New("callback source is not a named function")
				}
				return map[string]string{"Invoke": sourceType(body)}, nil
			}
			if shape.Kind != "fields" {
				continue
			}
			body, ok := spec.Type.(*ast.StructType)
			if !ok {
				return nil, errors.New("field source is not a struct")
			}
			for _, field := range body.Fields.List {
				if len(field.Names) == 0 {
					return nil, errors.New("embedded source fields require an explicit nested inventory")
				}
				for _, name := range field.Names {
					if ast.IsExported(name.Name) {
						result[name.Name] = sourceType(field.Type)
					}
				}
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%w from declared file", errSourceTypeAbsent)
	}
	if shape.Kind == "fields" {
		return result, nil
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		match, err := build.Default.MatchFile(filepath.Dir(path), entry.Name())
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		next, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(path), entry.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		if next.Name.Name != file.Name.Name {
			continue
		}
		for _, decl := range next.Decls {
			method, ok := decl.(*ast.FuncDecl)
			if !ok || method.Recv == nil || len(method.Recv.List) != 1 || !ast.IsExported(method.Name.Name) {
				continue
			}
			receiver := method.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			name, ok := receiver.(*ast.Ident)
			if ok && name.Name == shape.Type {
				result[method.Name.Name] = sourceType(method.Type)
			}
		}
	}
	return result, nil
}

func sourceType(node ast.Node) string {
	var out bytes.Buffer
	_ = format.Node(&out, token.NewFileSet(), node)
	return out.String()
}
