package main

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

type SourceFact struct {
	ID        int    `json:"id"`
	Component string `json:"component"`
	Shape     string `json:"shape"`
	Member    string `json:"member"`
}
type SourcePredicate struct {
	Fact     string `json:"fact"`
	Operator string `json:"operator"`
	Value    int64  `json:"value"`
}

func validateSourceFacts(reg Registry) error {
	ids := map[int]bool{}
	for key, fact := range reg.SourceFacts {
		member, ok := reg.SourceShapes[fact.Shape].Members[fact.Member]
		if !validToken(key) || fact.ID < 1 || fact.ID > math.MaxUint16 || ids[fact.ID] || !ok || !oneOf(member.Type, "int", "int64", "func() int", "func() int64", "func() time.Duration") {
			return fmt.Errorf("invalid source fact %s", key)
		}
		if _, ok := reg.Components[fact.Component]; !ok {
			return errors.New("source fact component is unknown")
		}
		if !contains(reg.SourceShapes[fact.Shape].Components, fact.Component) || member.Excluded != "" {
			return errors.New("source fact does not belong to an admitted source component")
		}
		ids[fact.ID] = true
	}
	for key, s := range reg.Signals {
		for _, variant := range s.Variants {
			seen := map[string]bool{}
			attrs := map[string]Binding{}
			for key, b := range reg.AttributeSets[variant.Base] {
				attrs[key] = b
			}
			for key, b := range variant.Attributes {
				attrs[key] = b
			}
			component := ""
			for key, b := range attrs {
				if reg.Attributes[key].Owner == "identity" {
					component = b.Const
				}
			}
			for _, predicate := range variant.When {
				fact, ok := reg.SourceFacts[predicate.Fact]
				if !ok || !oneOf(predicate.Operator, "gt", "eq") || predicate.Value < 0 || seen[predicate.Fact] || reg.Components[fact.Component].WireValue != component {
					return fmt.Errorf("signal %s has an invalid source presence predicate", key)
				}
				if !contains(s.Inputs, fact.Shape+"."+fact.Member) {
					return fmt.Errorf("signal %s predicate fact is not a declared input", key)
				}
				seen[predicate.Fact] = true
			}
		}
	}
	return nil
}
func shapeMember(reg Registry, reference string) (ShapeMember, bool) {
	shape, name, ok := strings.Cut(reference, ".")
	if !ok {
		return ShapeMember{}, false
	}
	member, ok := reg.SourceShapes[shape].Members[name]
	return member, ok
}
