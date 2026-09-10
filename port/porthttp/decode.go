package porthttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

const MaxEnvelopeBytes = 32 << 20

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
	if len(body) == 0 || len(body) > MaxEnvelopeBytes || !utf8.Valid(body) {
		return Envelope{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	envelope, ok := decodeEnvelope(decoder)
	if !ok {
		return Envelope{}, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Envelope{}, false
	}
	return envelope, true
}

func (this Envelope) Violations() []errs.Violation {
	out := make([]errs.Violation, 0, len(this.Errors.Validation)+len(this.Errors.General))
	out = append(out, this.Errors.Validation...)
	out = append(out, this.Errors.General...)
	return out
}

type wireViolation struct {
	Field         errs.Path `json:"field,omitempty"`
	Code          errs.Code `json:"error_code"`
	Message       string    `json:"message,omitempty"`
	MessageLocale string    `json:"message_locale,omitempty"`
}

type decodedGroups struct {
	validation []errs.Violation
	general    []errs.Violation
	total      int
}

func decodeEnvelope(decoder *json.Decoder) (Envelope, bool) {
	if !openDelim(decoder, '{') {
		return Envelope{}, false
	}
	var envelope Envelope
	var groups decodedGroups
	var typeSeen, partialSeen, errorsSeen bool
	for decoder.More() {
		key, ok := objectKey(decoder)
		if !ok {
			return Envelope{}, false
		}
		switch key {
		case "type":
			if typeSeen {
				return Envelope{}, false
			}
			typeSeen = true
			envelope.Type, ok = stringValue(decoder)
		case "partial":
			if partialSeen {
				return Envelope{}, false
			}
			partialSeen = true
			envelope.Partial, ok = boolValue(decoder)
		case "errors":
			if errorsSeen {
				return Envelope{}, false
			}
			errorsSeen = true
			groups, ok = decodeGroups(decoder)
		default:
			return Envelope{}, false
		}
		if !ok {
			return Envelope{}, false
		}
	}
	if !closeDelim(decoder, '}') || !typeSeen || envelope.Type != "error" || !errorsSeen || groups.total == 0 {
		return Envelope{}, false
	}
	envelope.Errors, envelope.Partial = boundedGroups(groups, envelope.Partial)
	return envelope, true
}

func decodeGroups(decoder *json.Decoder) (decodedGroups, bool) {
	if !openDelim(decoder, '{') {
		return decodedGroups{}, false
	}
	var groups decodedGroups
	var validationSeen, generalSeen bool
	for decoder.More() {
		key, ok := objectKey(decoder)
		if !ok {
			return decodedGroups{}, false
		}
		var violations []errs.Violation
		var total int
		switch key {
		case "validation":
			if validationSeen {
				return decodedGroups{}, false
			}
			validationSeen = true
			violations, total, ok = decodeViolations(decoder, true)
			groups.validation = violations
		case "general":
			if generalSeen {
				return decodedGroups{}, false
			}
			generalSeen = true
			violations, total, ok = decodeViolations(decoder, false)
			groups.general = violations
		default:
			return decodedGroups{}, false
		}
		if !ok {
			return decodedGroups{}, false
		}
		groups.total += total
	}
	if !closeDelim(decoder, '}') || !validationSeen && !generalSeen {
		return decodedGroups{}, false
	}
	return groups, true
}

func decodeViolations(decoder *json.Decoder, validation bool) ([]errs.Violation, int, bool) {
	if !openDelim(decoder, '[') {
		return nil, 0, false
	}
	violations := make([]errs.Violation, 0, 4)
	total := 0
	for decoder.More() {
		violation, ok := decodeViolation(decoder, validation)
		if !ok {
			return nil, 0, false
		}
		total++
		if len(violations) < MaxViolations {
			violations = append(violations, violation)
		}
	}
	if !closeDelim(decoder, ']') {
		return nil, 0, false
	}
	return violations, total, true
}

func decodeViolation(decoder *json.Decoder, validation bool) (errs.Violation, bool) {
	if !openDelim(decoder, '{') {
		return errs.Violation{}, false
	}
	var violation errs.Violation
	var fieldSeen, codeSeen, messageSeen, localeSeen bool
	for decoder.More() {
		key, ok := objectKey(decoder)
		if !ok {
			return errs.Violation{}, false
		}
		switch key {
		case "field":
			if fieldSeen {
				return errs.Violation{}, false
			}
			fieldSeen = true
			violation.Path, ok = pathValue(decoder)
		case "error_code":
			if codeSeen {
				return errs.Violation{}, false
			}
			codeSeen = true
			var code string
			code, ok = stringValue(decoder)
			violation.Code = errs.Code(code)
		case "message":
			if messageSeen {
				return errs.Violation{}, false
			}
			messageSeen = true
			violation.Message, ok = stringValue(decoder)
		case "message_locale":
			if localeSeen {
				return errs.Violation{}, false
			}
			localeSeen = true
			violation.MessageLocale, ok = stringValue(decoder)
		default:
			return errs.Violation{}, false
		}
		if !ok {
			return errs.Violation{}, false
		}
	}
	if !closeDelim(decoder, '}') || !codeSeen || !port.ValidErrorCode(violation.Code) {
		return errs.Violation{}, false
	}
	if validation != fieldSeen || validation && len(violation.Path) == 0 {
		return errs.Violation{}, false
	}
	if messageSeen && !port.ValidMessageText(violation.Message) {
		return errs.Violation{}, false
	}
	if localeSeen && (!messageSeen || violation.Message == "" || !port.ValidMessageLocale(violation.MessageLocale)) {
		return errs.Violation{}, false
	}
	return violation, true
}

func pathValue(decoder *json.Decoder) (errs.Path, bool) {
	if !openDelim(decoder, '[') {
		return nil, false
	}
	path := make(errs.Path, 0, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		switch value := token.(type) {
		case string:
			path = append(path, errs.Named(value))
		case json.Number:
			index, err := strconv.ParseInt(string(value), 10, 64)
			if err != nil || index < 0 || int64(int(index)) != index {
				return nil, false
			}
			path = append(path, errs.Indexed(int(index)))
		default:
			return nil, false
		}
	}
	if !closeDelim(decoder, ']') {
		return nil, false
	}
	return path, true
}

func boundedGroups(decoded decodedGroups, partial bool) (Groups, bool) {
	groups := Groups{}
	remaining := MaxViolations
	validation := min(len(decoded.validation), remaining)
	groups.Validation = exactViolations(decoded.validation, validation)
	remaining -= validation
	general := min(len(decoded.general), remaining)
	groups.General = exactViolations(decoded.general, general)
	return groups, partial || decoded.total > MaxViolations
}

func exactViolations(source []errs.Violation, count int) []errs.Violation {
	if count == 0 {
		return nil
	}
	out := make([]errs.Violation, count)
	copy(out, source[:count])
	return out
}

func objectKey(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	if err != nil {
		return "", false
	}
	key, ok := token.(string)
	return key, ok
}

func stringValue(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	if err != nil {
		return "", false
	}
	value, ok := token.(string)
	return value, ok
}

func boolValue(decoder *json.Decoder) (bool, bool) {
	token, err := decoder.Token()
	if err != nil {
		return false, false
	}
	value, ok := token.(bool)
	return value, ok
}

func openDelim(decoder *json.Decoder, want json.Delim) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	return ok && delim == want
}

func closeDelim(decoder *json.Decoder, want json.Delim) bool {
	return openDelim(decoder, want)
}
