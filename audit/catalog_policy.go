package audit

import (
	"fmt"
	"slices"
	"unicode/utf8"
)

type CalendarPeriod struct {
	Years  uint16
	Months uint8
	Days   uint16
}

type retentionRule struct {
	class   RetentionClass
	forever bool
	period  CalendarPeriod
}

type RetentionRule struct {
	value retentionRule
}

type RetentionRuleView struct {
	Class   RetentionClass
	Forever bool
	Period  CalendarPeriod
}

func KeepFor(class RetentionClass, period CalendarPeriod) RetentionRule {
	rule, err := TryKeepFor(class, period)
	if err != nil {
		panic(err)
	}
	return rule
}

func TryKeepFor(class RetentionClass, period CalendarPeriod) (RetentionRule, error) {
	if !validSemanticLabel(string(class)) {
		return RetentionRule{}, fmt.Errorf("%w: retention class is invalid", ErrDeclaration)
	}
	if !validCalendarPeriod(period) {
		return RetentionRule{}, fmt.Errorf("%w: retention period is invalid", ErrDeclaration)
	}
	return RetentionRule{value: retentionRule{class: class, period: period}}, nil
}

func KeepForever(class RetentionClass) RetentionRule {
	rule, err := TryKeepForever(class)
	if err != nil {
		panic(err)
	}
	return rule
}

func TryKeepForever(class RetentionClass) (RetentionRule, error) {
	if !validSemanticLabel(string(class)) {
		return RetentionRule{}, fmt.Errorf("%w: retention class is invalid", ErrDeclaration)
	}
	return RetentionRule{value: retentionRule{class: class, forever: true}}, nil
}

func RetentionRules(rules ...RetentionRule) []RetentionRule {
	return slices.Clone(rules)
}

func (r RetentionRule) view() RetentionRuleView {
	return RetentionRuleView{Class: r.value.class, Forever: r.value.forever, Period: r.value.period}
}

func validCalendarPeriod(period CalendarPeriod) bool {
	if period == (CalendarPeriod{}) || period.Months > 11 {
		return false
	}
	if period.Years > 100 || period.Years == 100 && (period.Months != 0 || period.Days != 0) {
		return false
	}
	return period.Days <= 36600
}

type SignatureDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type ProtectionDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type TokenDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type IdentityCommitmentDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type SemanticDigestDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type integrityPolicy struct {
	requiresSignature bool
	signature         SignatureDescription
}

type IntegrityPolicy struct {
	value integrityPolicy
}

type IntegrityPolicyView struct {
	RequiresSignature bool
	Signature         SignatureDescription
}

func IntegrityOnly() IntegrityPolicy {
	return IntegrityPolicy{value: integrityPolicy{}}
}

func RequireSignature(description SignatureDescription) IntegrityPolicy {
	if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
		panic(err)
	}
	return IntegrityPolicy{value: integrityPolicy{requiresSignature: true, signature: description}}
}

func (p IntegrityPolicy) view() IntegrityPolicyView {
	return IntegrityPolicyView{RequiresSignature: p.value.requiresSignature, Signature: p.value.signature}
}

func validateProviderDescription(algorithm, profile, keyID string) error {
	if !validProviderLabel(algorithm) || !validProviderLabel(profile) || !validProviderLabel(keyID) {
		return fmt.Errorf("%w: provider description is invalid", ErrDeclaration)
	}
	return nil
}

func validProviderLabel(value string) bool {
	if value == "" || len(value) > MaxNameBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validSemanticLabel(value string) bool {
	return validateCodecName(value) == nil
}
