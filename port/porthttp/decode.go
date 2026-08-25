package porthttp

import (
	"encoding/json"
	"net/http"

	"github.com/shardit-io/vv/errs"
)

// KindForStatus is [StatusFor] read backwards: the kind a client recovers from
// the status a service answered.
//
// It lives beside the table it inverts because the two have to agree, and two
// files would agree until the first time one of them gained a row. What it
// cannot recover is anything the forward table never distinguished — a status
// outside the table is [errs.KindInternal], which is what an unrecognised
// failure means on this side too.
//
// Every 2xx is [errs.KindInternal] as well, and that is not an oversight: this
// is only ever asked about a response that failed, and a success reaching it is
// a bug in the caller rather than a kind.
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
	default:
		return errs.KindInternal
	}
}

// ParseEnvelope reads a failure body back into the [Envelope] a renderer wrote.
//
// The second result is what makes it safe to use. It is false when the body is
// not an envelope at all, and a caller must treat that as a transport failure
// rather than as the status it arrived with. The case that forces it: a wrong
// base URL gets `404 page not found` in text/plain from http.ServeMux, and a
// client that read the status alone would report crud.ErrNotFound — turning a
// misconfiguration into "the row is not there", permanently and quietly, with
// nothing in the response to contradict it.
//
// [Envelope.Type] is checked for exactly this. It is always "error", so that a
// client can branch before parsing.
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

// Violations flattens the envelope's two groups back into one list.
//
// General first, which is the order errs.SortViolations put them in before
// [group] split them: an empty path is shorter than any other, and a shorter
// path sorts first. Restoring it means a gateway that decodes a failure and
// renders it again produces the same body it received.
func (e Envelope) Violations() []errs.Violation {
	out := make([]errs.Violation, 0, len(e.Errors.General)+len(e.Errors.Validation))
	out = append(out, e.Errors.General...)
	out = append(out, e.Errors.Validation...)
	return out
}

// The wire shapes. They exist rather than an UnmarshalJSON on errs.Violation
// because the decode is lossy and has to look it: three public fields out of
// seven reach a client ([[D-044]]), and a method that read like the inverse of
// Violation.MarshalJSON would hand back Origin, Params and Source as their zero
// values with nothing saying they were never sent. errs.Path is the opposite
// case and does have an UnmarshalJSON — a path is the whole of itself on the
// wire.
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
		// Origin is not set here and is not guessed here either. It is derived
		// from the kind, which this layer does not know, by port.FaultFrom.
		out = append(out, errs.Violation{Path: w.Field, Code: w.Code, Message: w.Message})
	}
	return out
}
