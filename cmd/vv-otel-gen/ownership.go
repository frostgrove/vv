package main

import (
	"errors"
	"fmt"
)

func validateVariantOwnership(reg Registry, variant Variant) error {
	bindings := map[string]Binding{}
	for key, binding := range reg.AttributeSets[variant.Base] {
		bindings[key] = binding
	}
	for key, binding := range variant.Attributes {
		bindings[key] = binding
	}
	var component Component
	selected := false
	for key, binding := range bindings {
		if reg.Attributes[key].Owner != "identity" {
			continue
		}
		if selected || binding.Const == "" || binding.Optional || binding.Declared || len(binding.Values) > 0 {
			return errors.New("variant requires one exact component identity")
		}
		for _, candidate := range reg.Components {
			if candidate.WireValue == binding.Const {
				component = candidate
				selected = true
				break
			}
		}
		if !selected {
			return errors.New("variant component is not registered")
		}
	}
	if !selected {
		return errors.New("variant has no resolved component identity")
	}
	for key, binding := range bindings {
		a, ok := reg.Attributes[key]
		if !ok {
			return fmt.Errorf("unknown attribute %s", key)
		}
		switch a.Owner {
		case "component":
			v, ok := vocabularies(component)[a.Vocabulary]
			if !ok || v.Domain == "" || binding.Domain != v.Domain {
				return fmt.Errorf("attribute %s domain %s does not belong to resolved component %s vocabulary %s", key, binding.Domain, component.WireValue, a.Vocabulary)
			}
		case "global":
			if !contains(reg.GlobalDomains, binding.Domain) {
				return fmt.Errorf("attribute %s uses a non-global domain", key)
			}
		case "declaration":
			if !binding.Declared {
				return fmt.Errorf("attribute %s requires an approved declaration", key)
			}
		case "identity":
		default:
			return fmt.Errorf("attribute %s has unknown ownership", key)
		}
	}
	return nil
}
