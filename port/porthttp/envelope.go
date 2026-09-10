package porthttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Envelope struct {
	Type string `json:"type"`

	Partial bool   `json:"partial,omitempty"`
	Errors  Groups `json:"errors"`
}

type Groups struct {
	Validation []errs.Violation `json:"validation,omitempty"`

	General []errs.Violation `json:"general,omitempty"`
}

func (this Envelope) MarshalJSON() ([]byte, error) {
	type wireGroups struct {
		Validation []wireViolation `json:"validation,omitempty"`
		General    []wireViolation `json:"general,omitempty"`
	}
	type wireEnvelope struct {
		Type    string     `json:"type"`
		Partial bool       `json:"partial,omitempty"`
		Errors  wireGroups `json:"errors"`
	}
	if this.Type != "error" {
		return nil, errors.New("porthttp: an envelope type must be error")
	}
	total := len(this.Errors.Validation) + len(this.Errors.General)
	if total == 0 {
		return nil, errors.New("porthttp: an envelope must carry at least one violation")
	}
	validationCount := min(len(this.Errors.Validation), MaxViolations)
	generalCount := min(len(this.Errors.General), MaxViolations-validationCount)
	validation, err := wireViolations(this.Errors.Validation[:validationCount], true)
	if err != nil {
		return nil, err
	}
	general, err := wireViolations(this.Errors.General[:generalCount], false)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(wireEnvelope{
		Type:    this.Type,
		Partial: this.Partial || total > MaxViolations,
		Errors: wireGroups{
			Validation: validation,
			General:    general,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxEnvelopeBytes {
		return nil, fmt.Errorf("porthttp: an envelope exceeds %d bytes", MaxEnvelopeBytes)
	}
	return raw, nil
}

func wireViolations(violations []errs.Violation, validation bool) ([]wireViolation, error) {
	if len(violations) == 0 {
		return nil, nil
	}
	out := make([]wireViolation, len(violations))
	for i, violation := range violations {
		if validation != (len(violation.Path) > 0) {
			return nil, fmt.Errorf("porthttp: violation %d is in the wrong envelope group", i)
		}
		for _, step := range violation.Path {
			if step.IsIndex && step.Index < 0 || !step.IsIndex && !utf8.ValidString(step.Name) {
				return nil, fmt.Errorf("porthttp: violation %d has an invalid field path", i)
			}
		}
		if !port.ValidErrorCode(violation.Code) {
			return nil, fmt.Errorf("porthttp: violation %d has an invalid error code", i)
		}
		if violation.Message != "" && !port.ValidMessageText(violation.Message) {
			return nil, fmt.Errorf("porthttp: violation %d has an invalid message", i)
		}
		if violation.MessageLocale != "" && (violation.Message == "" || !port.ValidMessageLocale(violation.MessageLocale)) {
			return nil, fmt.Errorf("porthttp: violation %d has incoherent message locale provenance", i)
		}
		out[i] = wireViolation{
			Field:         violation.Path,
			Code:          violation.Code,
			Message:       violation.Message,
			MessageLocale: violation.MessageLocale,
		}
	}
	return out, nil
}

func Internal() Envelope {
	return Envelope{
		Type: "error",
		Errors: Groups{
			General: []errs.Violation{{Code: errs.CodeInternal}},
		},
	}
}

func group(vs []errs.Violation) Groups {
	var g Groups
	for _, v := range vs {
		if len(v.Path) > 0 {
			g.Validation = append(g.Validation, v)
			continue
		}
		g.General = append(g.General, v)
	}
	return g
}
