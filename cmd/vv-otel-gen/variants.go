package main

import (
	"bytes"
	"fmt"
)

func renderVariantDescriptors(out *bytes.Buffer, reg Registry, manifest WireManifest) {
	out.WriteString("type SignalSourceFact uint16\ntype SignalSourceValue struct {Fact SignalSourceFact; Value int64}\ntype SignalSourceFacts []SignalSourceValue\ntype SignalSourcePredicate struct {Fact SignalSourceFact; Operator string; Value int64}\nconst (\n")
	for _, key := range sortedKeys(reg.SourceFacts) {
		fmt.Fprintf(out, "%s SignalSourceFact = %d\n", toCamelCase("SourceFact_"+key), reg.SourceFacts[key].ID)
	}
	out.WriteString(")\nfunc (f SignalSourceFact) Valid() bool { switch f {\n")
	for _, key := range sortedKeys(reg.SourceFacts) {
		fmt.Fprintf(out, "case %s: return true\n", toCamelCase("SourceFact_"+key))
	}
	out.WriteString("default: return false } }\n")
	out.WriteString("func signalSourceFacts(signal Signal) []SignalSourceFact { switch signal {\n")
	for _, key := range sortedKeys(reg.Signals) {
		fmt.Fprintf(out, "case %s: return []SignalSourceFact{", toCamelCase("Signal_"+key))
		for _, fact := range sortedKeys(reg.SourceFacts) {
			f := reg.SourceFacts[fact]
			if contains(reg.Signals[key].Inputs, f.Shape+"."+f.Member) {
				fmt.Fprintf(out, "%s,", toCamelCase("SourceFact_"+fact))
			}
		}
		out.WriteString("}\n")
	}
	out.WriteString("default: return nil } }\n")
	out.WriteString("type SignalAttributeDescriptor struct { Key attribute.Key; Type string; Values []string; Optional bool; Declared bool; MaxValues int; MaxBytes int; Charset string }\n")
	out.WriteString("type SignalVariantDescriptor struct { Attributes []SignalAttributeDescriptor; Absent []attribute.Key; Status string; When []SignalSourcePredicate }\n")
	out.WriteString("func signalVariants(signal Signal) []SignalVariantDescriptor { switch signal {\n")
	for _, key := range sortedKeys(manifest.Signals) {
		s := manifest.Signals[key]
		fmt.Fprintf(out, "case %s: return []SignalVariantDescriptor{\n", toCamelCase("Signal_"+key))
		for _, v := range s.ResolvedVariants {
			out.WriteString("{Attributes: []SignalAttributeDescriptor{")
			for _, name := range sortedKeys(v.Attributes) {
				a := v.Attributes[name]
				fmt.Fprintf(out, "{Key:%q, Type:%q, Optional:%t, Declared:%t, MaxValues:%d, MaxBytes:%d, Charset:%q, Values:[]string{", name, a.Type, a.Optional, a.Declared, a.MaxValues, a.MaxBytes, a.Charset)
				for _, value := range a.Values {
					fmt.Fprintf(out, "%q,", value)
				}
				out.WriteString("}},")
			}
			out.WriteString("}, Absent:[]attribute.Key{")
			for _, name := range v.Absent {
				fmt.Fprintf(out, "%q,", name)
			}
			fmt.Fprintf(out, "}, Status:%q, When:[]SignalSourcePredicate{", v.Status)
			for _, predicate := range v.When {
				fmt.Fprintf(out, "{Fact:%s, Operator:%q, Value:%d},", toCamelCase("SourceFact_"+predicate.Fact), predicate.Operator, predicate.Value)
			}
			out.WriteString("}},\n")
		}
		out.WriteString("}\n")
	}
	out.WriteString("default: return nil } }\n")
	out.WriteString(`
func matchesSignal(signal Signal, status string, attributes []attribute.KeyValue, facts SignalSourceFacts) bool {
	return signalFactsAdmitted(signal,facts) && matchesVariants(signalVariants(signal), status, attributes, facts)
}
func (s SignalDescriptor) Accepts(status string, attributes []attribute.KeyValue, facts ...SignalSourceValue) bool {
	return signalFactsAdmitted(s.SignalID,facts) && matchesVariants(s.Variants, status, attributes, facts)
}
func (s SignalDescriptor) AcceptsInt64(value int64) bool {
	if s.Kind!="metric" || s.NumberType!="int64" || s.HasMinimum&&value<s.MinimumInt64 || s.HasMaximum&&value>s.MaximumInt64 {return false}
	return s.RecordWhen=="non_negative"&&value>=0 || s.RecordWhen=="positive"&&value>0
}
func (s SignalDescriptor) AcceptsFloat64(value float64) bool {
	if s.Kind!="metric" || s.NumberType!="float64" || math.IsNaN(value) || math.IsInf(value,0) || s.HasMinimum&&value<s.Minimum || s.HasMaximum&&value>s.Maximum {return false}
	return s.RecordWhen=="non_negative"&&value>=0 || s.RecordWhen=="positive"&&value>0
}
func (s SignalDescriptor) AcceptsValue(value float64) bool {
	if s.NumberType=="float64" {return s.AcceptsFloat64(value)}
	if s.NumberType!="int64" || math.IsNaN(value) || math.IsInf(value,0) || value != math.Trunc(value) || value < -9007199254740991 || value > 9007199254740991 {return false}
	return s.AcceptsInt64(int64(value))
}
func signalFactsAdmitted(signal Signal,facts SignalSourceFacts) bool {
	allowed:=signalSourceFacts(signal)
	for _,fact:=range facts {
		found:=false
		for _,candidate:=range allowed {if fact.Fact==candidate {found=true;break}}
		if !found {return false}
	}
	return signal.Valid()
}
func matchesVariants(variants []SignalVariantDescriptor, status string, attributes []attribute.KeyValue, facts SignalSourceFacts) bool {
	for i,fact:=range facts {
		if !fact.Fact.Valid() || fact.Value < 0 {return false}
		for _,prior:=range facts[:i] {if prior.Fact==fact.Fact {return false}}
	}
	for i, a := range attributes {
		for _, prior := range attributes[:i] {
			if prior.Key == a.Key { return false }
		}
	}
	matches:=0
	for _, variant := range variants {
		if variant.Status != status { continue }
		if !sourcePredicatesMatch(variant.When,facts) {continue}
		matched := true
		for _, a := range attributes {
			found := false
			for _, spec := range variant.Attributes {
				if spec.Key == a.Key { found = signalAttributeAccepts(spec, a.Value); break }
			}
			if !found { matched = false; break }
		}
		if !matched { continue }
		for _, spec := range variant.Attributes {
			if spec.Optional { continue }
			found := false
			for _, a := range attributes { if a.Key == spec.Key { found = true; break } }
			if !found { matched = false; break }
		}
		if !matched { continue }
		for _, key := range variant.Absent {
			for _, a := range attributes { if a.Key == key { matched = false; break } }
		}
		if matched { matches++ }
	}
	return matches==1
}
func sourcePredicatesMatch(predicates []SignalSourcePredicate, facts SignalSourceFacts) bool {
	for _,predicate:=range predicates {
		matched:=false
		for _,fact:=range facts {
			if fact.Fact==predicate.Fact {matched=predicate.Operator=="gt"&&fact.Value>predicate.Value||predicate.Operator=="eq"&&fact.Value==predicate.Value;break}
		}
		if !matched {return false}
	}
	return true
}
func signalAttributeAccepts(spec SignalAttributeDescriptor, value attribute.Value) bool {
	if spec.Type == "string" && value.Type() != attribute.STRING ||
		spec.Type == "bool" && value.Type() != attribute.BOOL ||
		spec.Type == "int64" && value.Type() != attribute.INT64 { return false }
	rendered := value.Emit()
	if spec.Declared {
		if spec.Type != "string" || len(rendered) == 0 || len(rendered) > spec.MaxBytes || !utf8.ValidString(rendered) || spec.Charset != "unicode_letter_digit_dot_underscore_hyphen" { return false }
		for _, r := range rendered {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' { return false }
		}
		return true
	}
	for _, allowed := range spec.Values { if rendered == allowed { return true } }
	return false
}
`)
}
