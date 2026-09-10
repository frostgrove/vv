package porthttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func TestTheHTTPEnvelopeCarriesValidatedPerViolationLocaleProvenance(t *testing.T) {
	original := Envelope{
		Type: "error",
		Errors: Groups{
			Validation: []errs.Violation{{
				Path:          errs.Path{errs.Named("user"), errs.Named("email")},
				Code:          errs.CodeUnique,
				Message:       "déjà pris",
				MessageLocale: "fr",
				Origin:        errs.OriginState,
				Params:        errs.P{"constraint": "users_email_key"},
			}},
			General: []errs.Violation{{Code: errs.CodeInternal}},
		},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"message_locale":"fr"`)) {
		t.Fatalf("localized envelope = %s, want per-violation locale", raw)
	}
	for _, private := range []string{"users_email_key", "Origin", "Params", "Source", "Approximate"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("localized envelope exported private %q: %s", private, raw)
		}
	}

	parsed, ok := ParseEnvelope(raw)
	if !ok {
		t.Fatalf("the renderer's own envelope was refused: %s", raw)
	}
	got := parsed.Errors.Validation[0]
	if got.Path.String() != "user.email" || got.Code != errs.CodeUnique || got.Message != "déjà pris" || got.MessageLocale != "fr" {
		t.Fatalf("localized violation roundtrip = %+v", got)
	}
	if got.Origin != errs.OriginInput || got.Params != nil || got.Source.Constraint != "" || got.Approximate {
		t.Fatalf("private fields crossed the wire: %+v", got)
	}

	original.Errors.Validation[0].MessageLocale = "fr_CA"
	if raw, err = json.Marshal(original); err == nil {
		t.Fatalf("invalid locale provenance was emitted: %s", raw)
	}
}

func TestEnvelopeMarshalIsCanonicalBoundedAndParseable(t *testing.T) {
	original := Envelope{Type: "error"}
	for i := 0; i < MaxViolations-1; i++ {
		original.Errors.Validation = append(original.Errors.Validation, errs.Violation{
			Path: errs.Path{errs.Named("v" + strconv.Itoa(i))}, Code: errs.CodeRequired,
		})
	}
	for i := 0; i < 2; i++ {
		original.Errors.General = append(original.Errors.General, errs.Violation{Code: errs.Code("g" + strconv.Itoa(i))})
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := ParseEnvelope(raw)
	if !ok {
		t.Fatalf("canonical marshal output was refused: %s", raw)
	}
	if len(decoded.Errors.Validation) != MaxViolations-1 || len(decoded.Errors.General) != 1 || !decoded.Partial {
		t.Fatalf("marshal cap = validation:%d general:%d partial:%v", len(decoded.Errors.Validation), len(decoded.Errors.General), decoded.Partial)
	}
	if got := decoded.Errors.General[0].Code; got != "g0" {
		t.Fatalf("canonical cap kept %q, want g0", got)
	}
}

func TestEnvelopeMarshalRefusesAnythingItsDecoderWouldRefuse(t *testing.T) {
	validGeneral := errs.Violation{Code: errs.CodeInternal}
	validValidation := errs.Violation{Path: errs.Path{errs.Named("email")}, Code: errs.CodeRequired}
	cases := map[string]Envelope{
		"wrong type":                 {Type: "problem", Errors: Groups{General: []errs.Violation{validGeneral}}},
		"empty groups":               {Type: "error"},
		"validation without field":   {Type: "error", Errors: Groups{Validation: []errs.Violation{validGeneral}}},
		"general with field":         {Type: "error", Errors: Groups{General: []errs.Violation{validValidation}}},
		"negative path index":        {Type: "error", Errors: Groups{Validation: []errs.Violation{{Path: errs.Path{errs.Indexed(-1)}, Code: errs.CodeRequired}}}},
		"invalid path UTF-8":         {Type: "error", Errors: Groups{Validation: []errs.Violation{{Path: errs.Path{errs.Named(string([]byte{0xff}))}, Code: errs.CodeRequired}}}},
		"empty code":                 {Type: "error", Errors: Groups{General: []errs.Violation{{}}}},
		"overlong code":              {Type: "error", Errors: Groups{General: []errs.Violation{{Code: errs.Code(strings.Repeat("x", errs.MaxMessageKeyBytes+1))}}}},
		"invalid code UTF-8":         {Type: "error", Errors: Groups{General: []errs.Violation{{Code: errs.Code(string([]byte{0xff}))}}}},
		"overlong message":           {Type: "error", Errors: Groups{General: []errs.Violation{{Code: errs.CodeInternal, Message: strings.Repeat("x", errs.MaxMessageOutputBytes+1)}}}},
		"invalid message UTF-8":      {Type: "error", Errors: Groups{General: []errs.Violation{{Code: errs.CodeInternal, Message: string([]byte{0xff})}}}},
		"locale without message":     {Type: "error", Errors: Groups{General: []errs.Violation{{Code: errs.CodeInternal, MessageLocale: "fr"}}}},
		"invalid message locale":     {Type: "error", Errors: Groups{General: []errs.Violation{{Code: errs.CodeInternal, Message: "x", MessageLocale: "fr_CA"}}}},
		"envelope beyond body bound": {Type: "error", Errors: Groups{Validation: []errs.Violation{{Path: errs.Path{errs.Named(strings.Repeat("x", MaxEnvelopeBytes))}, Code: errs.CodeRequired}}}},
	}
	for name, envelope := range cases {
		t.Run(name, func(t *testing.T) {
			if raw, err := json.Marshal(envelope); err == nil {
				t.Fatalf("invalid envelope was marshaled: %s", raw)
			}
		})
	}
}

func TestParseEnvelopeAcceptsOnlyTheOwnedCanonicalShape(t *testing.T) {
	valid := []string{
		`{"type":"error","errors":{"general":[{"error_code":"internal"}]}}`,
		`{"errors":{"validation":[{"message":"required","error_code":"required","field":["items",3,"name"]}]},"partial":false,"type":"error"}`,
		`{"type":"error","errors":{"validation":[],"general":[{"message_locale":"fr-CA","message":"indisponible","error_code":"retryable"}]}}`,
	}
	for _, body := range valid {
		if _, ok := ParseEnvelope([]byte(body)); !ok {
			t.Fatalf("canonical envelope was refused: %s", body)
		}
	}

	invalid := map[string][]byte{
		"foreign type-only body":      []byte(`{"type":"error","message":"no route"}`),
		"wrong type":                  []byte(`{"type":"problem","errors":{"general":[{"error_code":"internal"}]}}`),
		"missing type":                []byte(`{"errors":{"general":[{"error_code":"internal"}]}}`),
		"missing errors":              []byte(`{"type":"error"}`),
		"empty errors object":         []byte(`{"type":"error","errors":{}}`),
		"empty groups":                []byte(`{"type":"error","errors":{"validation":[],"general":[]}}`),
		"null group":                  []byte(`{"type":"error","errors":{"general":null}}`),
		"validation without field":    []byte(`{"type":"error","errors":{"validation":[{"error_code":"required"}]}}`),
		"validation with empty field": []byte(`{"type":"error","errors":{"validation":[{"field":[],"error_code":"required"}]}}`),
		"general with field":          []byte(`{"type":"error","errors":{"general":[{"field":["name"],"error_code":"required"}]}}`),
		"missing code":                []byte(`{"type":"error","errors":{"general":[{"message":"no"}]}}`),
		"empty code":                  []byte(`{"type":"error","errors":{"general":[{"error_code":""}]}}`),
		"overlong code":               []byte(`{"type":"error","errors":{"general":[{"error_code":"` + strings.Repeat("x", errs.MaxMessageKeyBytes+1) + `"}]}}`),
		"empty message":               []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":""}]}}`),
		"locale without message":      []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message_locale":"fr"}]}}`),
		"locale with empty message":   []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":"","message_locale":"fr"}]}}`),
		"empty locale":                []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":"x","message_locale":""}]}}`),
		"invalid locale":              []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":"x","message_locale":"fr_CA"}]}}`),
		"overlong locale":             []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":"x","message_locale":"` + strings.Repeat("a", errs.MaxLocaleBytes+1) + `"}]}}`),
		"wrong locale type":           []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":"x","message_locale":3}]}}`),
		"negative path index":         []byte(`{"type":"error","errors":{"validation":[{"field":[-1],"error_code":"required"}]}}`),
		"fractional path index":       []byte(`{"type":"error","errors":{"validation":[{"field":[1.5],"error_code":"required"}]}}`),
		"nested path value":           []byte(`{"type":"error","errors":{"validation":[{"field":[["name"]],"error_code":"required"}]}}`),
		"unknown envelope member":     []byte(`{"type":"error","trace":"x","errors":{"general":[{"error_code":"internal"}]}}`),
		"unknown group member":        []byte(`{"type":"error","errors":{"other":[{"error_code":"internal"}]}}`),
		"unknown violation member":    []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","detail":"secret"}]}}`),
		"null partial":                []byte(`{"type":"error","partial":null,"errors":{"general":[{"error_code":"internal"}]}}`),
		"trailing document":           []byte(`{"type":"error","errors":{"general":[{"error_code":"internal"}]}} {}`),
		"invalid UTF-8":               append([]byte(`{"type":"error","errors":{"general":[{"error_code":"`), 0xff),
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			if envelope, ok := ParseEnvelope(body); ok {
				t.Fatalf("malformed or foreign body was accepted as %+v: %q", envelope, body)
			}
		})
	}
}

func TestInboundMessageLimitHasAnExactBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		ok      bool
	}{
		{"N", strings.Repeat("x", errs.MaxMessageOutputBytes), true},
		{"N plus one", strings.Repeat("x", errs.MaxMessageOutputBytes+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"type":"error","errors":{"general":[{"error_code":"internal","message":` + strconv.Quote(tc.message) + `}]}}`)
			envelope, ok := ParseEnvelope(body)
			if ok != tc.ok {
				t.Fatalf("message of %d bytes accepted=%v, want %v", len(tc.message), ok, tc.ok)
			}
			if ok && envelope.Errors.General[0].Message != tc.message {
				t.Fatalf("message at N changed length to %d", len(envelope.Errors.General[0].Message))
			}
		})
	}
}

func TestAFlatPathWithinTheEnvelopeBudgetRoundTripsWithoutASeparateDepthLimit(t *testing.T) {
	path := make(errs.Path, 0, 4096)
	for i := 0; i < cap(path); i++ {
		if i%2 == 0 {
			path = append(path, errs.Named("field"+strconv.Itoa(i)))
		} else {
			path = append(path, errs.Indexed(i))
		}
	}
	original := Envelope{
		Type: "error",
		Errors: Groups{Validation: []errs.Violation{{
			Path: path, Code: errs.CodeRequired, Message: "required",
		}}},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= MaxEnvelopeBytes {
		t.Fatalf("path fixture is %d bytes and does not exercise the within-budget contract", len(raw))
	}
	decoded, ok := ParseEnvelope(raw)
	if !ok {
		t.Fatal("a flat path emitted within MaxEnvelopeBytes was refused")
	}
	if !reflect.DeepEqual(decoded.Errors.Validation[0].Path, path) {
		t.Fatalf("path of %d steps changed to %d", len(path), len(decoded.Errors.Validation[0].Path))
	}
}

func TestParseEnvelopeRejectsDuplicateMembersAtEveryOwnedObjectLevel(t *testing.T) {
	cases := map[string]string{
		"envelope type":            `{"type":"error","\u0074ype":"error","errors":{"general":[{"error_code":"internal"}]}}`,
		"envelope partial":         `{"type":"error","partial":false,"partial":true,"errors":{"general":[{"error_code":"internal"}]}}`,
		"envelope errors":          `{"type":"error","errors":{"general":[{"error_code":"internal"}]},"errors":{"general":[{"error_code":"internal"}]}}`,
		"group validation":         `{"type":"error","errors":{"validation":[{"field":["a"],"error_code":"required"}],"validation":[{"field":["b"],"error_code":"required"}]}}`,
		"group general":            `{"type":"error","errors":{"general":[{"error_code":"internal"}],"general":[{"error_code":"internal"}]}}`,
		"violation field":          `{"type":"error","errors":{"validation":[{"field":["a"],"field":["b"],"error_code":"required"}]}}`,
		"violation code":           `{"type":"error","errors":{"general":[{"error_code":"internal","error_code":"conflict"}]}}`,
		"violation message":        `{"type":"error","errors":{"general":[{"error_code":"internal","message":"a","message":"b"}]}}`,
		"violation message locale": `{"type":"error","errors":{"general":[{"error_code":"internal","message":"a","message_locale":"fr","message_locale":"de"}]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParseEnvelope([]byte(body)); ok {
				t.Fatalf("duplicate member was accepted: %s", body)
			}
		})
	}
}

func TestInboundViolationLimitIsGlobalHonestAndOrderIndependent(t *testing.T) {
	for _, tc := range []struct {
		count   int
		partial bool
	}{
		{MaxViolations, false},
		{MaxViolations + 1, true},
	} {
		body := groupedEnvelope(tc.count, 0, false, false)
		envelope, ok := ParseEnvelope(body)
		if !ok {
			t.Fatalf("%d canonical violations were refused", tc.count)
		}
		if got := len(envelope.Violations()); got != MaxViolations {
			t.Fatalf("%d inbound violations reconstructed %d, want %d", tc.count, got, MaxViolations)
		}
		if envelope.Partial != tc.partial {
			t.Fatalf("%d inbound violations produced partial=%v, want %v", tc.count, envelope.Partial, tc.partial)
		}
	}

	forward, ok := ParseEnvelope(groupedEnvelope(MaxViolations-1, 2, false, false))
	if !ok {
		t.Fatal("split groups were refused")
	}
	reversed, ok := ParseEnvelope(groupedEnvelope(MaxViolations-1, 2, true, false))
	if !ok {
		t.Fatal("reversed split groups were refused")
	}
	for _, envelope := range []Envelope{forward, reversed} {
		if len(envelope.Errors.Validation) != MaxViolations-1 || len(envelope.Errors.General) != 1 || !envelope.Partial {
			t.Fatalf("split group cap = validation:%d general:%d partial:%v", len(envelope.Errors.Validation), len(envelope.Errors.General), envelope.Partial)
		}
		if cap(envelope.Errors.Validation)+cap(envelope.Errors.General) != MaxViolations {
			t.Fatalf("bounded groups retain capacity for %d violations", cap(envelope.Errors.Validation)+cap(envelope.Errors.General))
		}
		violations := envelope.Violations()
		if violations[0].Code != "v000" || violations[MaxViolations-2].Code != "v098" || violations[MaxViolations-1].Code != "g000" {
			t.Fatalf("wire order after global cap = %q ... %q, %q", violations[0].Code, violations[MaxViolations-2].Code, violations[MaxViolations-1].Code)
		}
	}
	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("JSON member order changed the decoded envelope\nforward: %+v\nreverse: %+v", forward, reversed)
	}

	declared, ok := ParseEnvelope(groupedEnvelope(1, 0, false, true))
	if !ok || !declared.Partial {
		t.Fatalf("declared partial marker was lost: %+v, %v", declared, ok)
	}
}

func TestEnvelopeBodyLimitHasAnExactBoundary(t *testing.T) {
	body := []byte(`{"type":"error","errors":{"general":[{"error_code":"internal"}]}}`)
	atLimit := append(append([]byte{}, body...), bytes.Repeat([]byte{' '}, MaxEnvelopeBytes-len(body))...)
	if _, ok := ParseEnvelope(atLimit); !ok {
		t.Fatal("an envelope exactly at MaxEnvelopeBytes was refused")
	}
	if _, ok := ParseEnvelope(append(atLimit, ' ')); ok {
		t.Fatal("an envelope one byte past MaxEnvelopeBytes was accepted")
	}
}

func FuzzParseEnvelope(f *testing.F) {
	f.Add([]byte(`{"type":"error","errors":{"general":[{"error_code":"internal"}]}}`))
	f.Add(groupedEnvelope(MaxViolations+1, 1, true, false))
	f.Fuzz(func(t *testing.T, body []byte) {
		envelope, ok := ParseEnvelope(body)
		if !ok {
			return
		}
		if len(envelope.Violations()) > MaxViolations {
			t.Fatalf("accepted envelope reconstructed %d violations", len(envelope.Violations()))
		}
		for _, violation := range envelope.Violations() {
			if violation.MessageLocale != "" && violation.Message == "" {
				t.Fatalf("locale %q survived without a message", violation.MessageLocale)
			}
		}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := ParseEnvelope(raw); !ok {
			t.Fatalf("accepted envelope did not survive canonical encoding: %s", raw)
		}
	})
}

func groupedEnvelope(validation, general int, reverse, partial bool) []byte {
	var body strings.Builder
	body.WriteString(`{"type":"error"`)
	if partial {
		body.WriteString(`,"partial":true`)
	}
	body.WriteString(`,"errors":{`)
	writeGroup := func(name, prefix string, count int, field bool) {
		body.WriteString(strconv.Quote(name))
		body.WriteString(`:[`)
		for i := 0; i < count; i++ {
			if i > 0 {
				body.WriteByte(',')
			}
			body.WriteByte('{')
			if field {
				fmt.Fprintf(&body, `"field":["f%03d"],`, i)
			}
			fmt.Fprintf(&body, `"error_code":"%s%03d"}`, prefix, i)
		}
		body.WriteByte(']')
	}
	if reverse {
		writeGroup("general", "g", general, false)
		body.WriteByte(',')
		writeGroup("validation", "v", validation, true)
	} else {
		writeGroup("validation", "v", validation, true)
		body.WriteByte(',')
		writeGroup("general", "g", general, false)
	}
	body.WriteString(`}}`)
	return []byte(body.String())
}
