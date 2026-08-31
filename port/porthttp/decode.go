package porthttp

import (
	"encoding/json"
	"net/http"

	"github.com/frostgrove/vv/errs"
)

func KindForStatus(code int) errs.Kind {
	switch code {
	case http.StatusNotFound:
		return errs.KindNotFound
	case http.StatusUnauthorized:
		return errs.KindUnauthorized
	case http.StatusForbidden:
		return errs.KindForbidden
	case http.StatusServiceUnavailable:
		return errs.KindRetryable
	case http.StatusConflict:
		return errs.KindConflict
	case http.StatusUnprocessableEntity:
		return errs.KindValidation
	case http.StatusBadRequest:
		return errs.KindBadRequest
	case http.StatusRequestEntityTooLarge:
		return errs.KindTooLarge
	case http.StatusMethodNotAllowed:
		return errs.KindMethodNotAllowed
	default:
		return errs.KindInternal
	}
}

func ParseEnvelope(body []byte) (Envelope, bool) {
	var w wireEnvelope
	if err := json.Unmarshal(body, &w); err != nil {
		return Envelope{}, false
	}
	if w.Type != "error" {
		return Envelope{}, false
	}
	return Envelope{
		Type:    w.Type,
		Partial: w.Partial,
		Errors: Groups{
			Validation: violationsOf(w.Errors.Validation),
			General:    violationsOf(w.Errors.General),
		},
	}, true
}

func (this Envelope) Violations() []errs.Violation {
	out := make([]errs.Violation, 0, len(this.Errors.General)+len(this.Errors.Validation))
	out = append(out, this.Errors.General...)
	out = append(out, this.Errors.Validation...)
	return out
}

type wireEnvelope struct {
	Type    string `json:"type"`
	Partial bool   `json:"partial"`
	Errors  struct {
		Validation []wireViolation `json:"validation"`
		General    []wireViolation `json:"general"`
	} `json:"errors"`
}

type wireViolation struct {
	Field   errs.Path `json:"field"`
	Code    errs.Code `json:"error_code"`
	Message string    `json:"message"`
}

func violationsOf(ws []wireViolation) []errs.Violation {
	if len(ws) == 0 {
		return nil
	}
	out := make([]errs.Violation, 0, len(ws))
	for _, w := range ws {
		out = append(out, errs.Violation{Path: w.Field, Code: w.Code, Message: w.Message})
	}
	return out
}
