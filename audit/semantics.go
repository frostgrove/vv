package audit

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

type PolicyFixtureKind uint8

const (
	DeclarationPolicyFixture PolicyFixtureKind = iota + 1
	AttemptStartPolicyFixture
	AttemptCheckpointPolicyFixture
	AttemptFinishPolicyFixture
)

type semanticGolden struct {
	name        FixtureName
	fingerprint PolicyFixtureFingerprint
	kind        PolicyFixtureKind
	transition  AttemptTransitionKind
	checkpoint  AttemptCheckpointCode
	reason      Reason
}

type SemanticGolden struct {
	value semanticGolden
}

type policySemantics struct {
	version PolicyVersion
	goldens []semanticGolden
}

type PolicySemantics struct {
	value policySemantics
}

type PolicySemanticsDescription struct {
	Version     PolicyVersion
	Fingerprint PolicyFingerprint
	Fixtures    []PolicyGoldenDescription
}

type PolicyGoldenDescription struct {
	Name        FixtureName
	Fingerprint PolicyFixtureFingerprint
	Kind        PolicyFixtureKind
	Transition  AttemptTransitionKind
	Checkpoint  AttemptCheckpointCode
	Reason      Reason
}

func Semantics(version PolicyVersion, goldens ...SemanticGolden) PolicySemantics {
	semantics, err := TrySemantics(version, goldens...)
	if err != nil {
		panic(err)
	}
	return semantics
}

func TrySemantics(version PolicyVersion, goldens ...SemanticGolden) (PolicySemantics, error) {
	if version == 0 {
		return PolicySemantics{}, fmt.Errorf("%w: policy version is zero", ErrDeclaration)
	}
	if len(goldens) > MaxPolicyGoldens {
		return PolicySemantics{}, fmt.Errorf("%w: policy golden count exceeds %d", ErrTooLarge, MaxPolicyGoldens)
	}
	values := make([]semanticGolden, len(goldens))
	names := make(map[FixtureName]struct{}, len(goldens))
	for index, golden := range goldens {
		value := golden.value
		if err := validateSemanticGolden(value); err != nil {
			return PolicySemantics{}, err
		}
		if _, duplicate := names[value.name]; duplicate {
			return PolicySemantics{}, fmt.Errorf("%w: duplicate policy fixture name", ErrDeclaration)
		}
		names[value.name] = struct{}{}
		values[index] = value
	}
	slices.SortFunc(values, compareSemanticGolden)
	return PolicySemantics{value: policySemantics{version: version, goldens: values}}, nil
}

func PolicyGolden(name FixtureName, expected string) SemanticGolden {
	golden, err := TryPolicyGolden(name, expected)
	if err != nil {
		panic(err)
	}
	return golden
}

func TryPolicyGolden(name FixtureName, expected string) (SemanticGolden, error) {
	return trySemanticGolden(name, expected, DeclarationPolicyFixture, 0, "", "")
}

func AttemptStartGolden(name FixtureName, expected string) SemanticGolden {
	golden, err := TryAttemptStartGolden(name, expected)
	if err != nil {
		panic(err)
	}
	return golden
}

func TryAttemptStartGolden(name FixtureName, expected string) (SemanticGolden, error) {
	return trySemanticGolden(name, expected, AttemptStartPolicyFixture, AttemptStartedTransition, "", "")
}

func AttemptCheckpointGolden(name FixtureName, checkpoint AttemptCheckpointCode, expected string) SemanticGolden {
	golden, err := TryAttemptCheckpointGolden(name, checkpoint, expected)
	if err != nil {
		panic(err)
	}
	return golden
}

func TryAttemptCheckpointGolden(name FixtureName, checkpoint AttemptCheckpointCode, expected string) (SemanticGolden, error) {
	return trySemanticGolden(name, expected, AttemptCheckpointPolicyFixture, AttemptCheckpointTransition, checkpoint, "")
}

func AttemptFinishGolden(name FixtureName, transition AttemptTransitionKind, reason Reason, expected string) SemanticGolden {
	golden, err := TryAttemptFinishGolden(name, transition, reason, expected)
	if err != nil {
		panic(err)
	}
	return golden
}

func TryAttemptFinishGolden(name FixtureName, transition AttemptTransitionKind, reason Reason, expected string) (SemanticGolden, error) {
	return trySemanticGolden(name, expected, AttemptFinishPolicyFixture, transition, "", reason)
}

func trySemanticGolden(name FixtureName, expected string, kind PolicyFixtureKind, transition AttemptTransitionKind, checkpoint AttemptCheckpointCode, reason Reason) (SemanticGolden, error) {
	if err := validateCodecName(string(name)); err != nil {
		return SemanticGolden{}, fmt.Errorf("%w: policy fixture name is invalid", ErrDeclaration)
	}
	if len(expected) != 64 || expected != strings.ToLower(expected) {
		return SemanticGolden{}, fmt.Errorf("%w: policy fixture fingerprint must be lowercase SHA-256", ErrDeclaration)
	}
	wire, err := hex.DecodeString(expected)
	if err != nil {
		return SemanticGolden{}, fmt.Errorf("%w: policy fixture fingerprint must be lowercase SHA-256", ErrDeclaration)
	}
	var fingerprint PolicyFixtureFingerprint
	copy(fingerprint[:], wire)
	value := semanticGolden{name: name, fingerprint: fingerprint, kind: kind, transition: transition, checkpoint: checkpoint, reason: reason}
	if err := validateSemanticGolden(value); err != nil {
		return SemanticGolden{}, err
	}
	return SemanticGolden{value: value}, nil
}

func validateSemanticGolden(value semanticGolden) error {
	if value.name == "" || value.fingerprint == (PolicyFixtureFingerprint{}) {
		return fmt.Errorf("%w: policy fixture is incomplete", ErrDeclaration)
	}
	switch value.kind {
	case DeclarationPolicyFixture:
		if value.transition != 0 || value.checkpoint != "" || value.reason != "" {
			return fmt.Errorf("%w: declaration fixture has attempt coordinates", ErrDeclaration)
		}
	case AttemptStartPolicyFixture:
		if value.transition != AttemptStartedTransition || value.checkpoint != "" || value.reason != "" {
			return fmt.Errorf("%w: attempt start fixture is malformed", ErrDeclaration)
		}
	case AttemptCheckpointPolicyFixture:
		if value.transition != AttemptCheckpointTransition || value.checkpoint == "" || value.reason != "" {
			return fmt.Errorf("%w: attempt checkpoint fixture is malformed", ErrDeclaration)
		}
	case AttemptFinishPolicyFixture:
		if !terminalAttemptTransition(value.transition) || value.checkpoint != "" {
			return fmt.Errorf("%w: attempt finish fixture is malformed", ErrDeclaration)
		}
		if value.transition == AttemptSucceededTransition && value.reason != "" {
			return fmt.Errorf("%w: successful attempt fixture has a reason", ErrDeclaration)
		}
	default:
		return fmt.Errorf("%w: policy fixture kind is invalid", ErrDeclaration)
	}
	return nil
}

func terminalAttemptTransition(value AttemptTransitionKind) bool {
	return value == AttemptOutcomeUnknownTransition || value == AttemptSucceededTransition || value == AttemptFailedTransition || value == AttemptCancelledTransition || value == AttemptAbandonedTransition
}

func compareSemanticGolden(left, right semanticGolden) int {
	if left.kind != right.kind {
		return int(left.kind) - int(right.kind)
	}
	if left.transition != right.transition {
		return int(left.transition) - int(right.transition)
	}
	if left.checkpoint != right.checkpoint {
		return strings.Compare(string(left.checkpoint), string(right.checkpoint))
	}
	if left.reason != right.reason {
		return strings.Compare(string(left.reason), string(right.reason))
	}
	return strings.Compare(string(left.name), string(right.name))
}

func (p PolicySemantics) description(fingerprint PolicyFingerprint) PolicySemanticsDescription {
	fixtures := make([]PolicyGoldenDescription, len(p.value.goldens))
	for index, golden := range p.value.goldens {
		fixtures[index] = PolicyGoldenDescription{
			Name:        golden.name,
			Fingerprint: golden.fingerprint,
			Kind:        golden.kind,
			Transition:  golden.transition,
			Checkpoint:  golden.checkpoint,
			Reason:      golden.reason,
		}
	}
	return PolicySemanticsDescription{Version: p.value.version, Fingerprint: fingerprint, Fixtures: fixtures}
}
