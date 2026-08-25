package porthttp

import "github.com/shardit-io/vv/errs"

// An Envelope is the only body this library puts on the wire for a failed
// request:
//
//	{"type":"error","errors":{"validation":[{"field":["user","email"],"error_code":"unique","message":"…"}]}}
//
// Type is always "error", so a client can branch before parsing. The group says
// what the client can act on rather than where the failure came from — which is
// why a 409 unique conflict appears under validation: it names a field the
// client sent, so a form can mark it.
//
// RFC 9457 problem+json is not shipped, and neither is the older
// {"error":…,"path":…,"message":…} body. Two shapes is twice the surface to
// test, document and keep honest for a choice almost nobody changes; the
// [Renderer] seam is there for a consumer who does.
type Envelope struct {
	Type string `json:"type"`
	// Partial says a cap was hit and the list is incomplete. A response that
	// lists four violations without it is claiming there were four.
	Partial bool   `json:"partial,omitempty"`
	Errors  Groups `json:"errors"`
}

// Groups are the envelope's two buckets. A struct rather than a map, so the key
// order is this declaration rather than the encoder's habit — the same reason
// [[D-014]] gives for everything else here being byte-identical run to run.
type Groups struct {
	// Validation holds every violation that names a field.
	Validation []errs.Violation `json:"validation,omitempty"`
	// General holds every violation that does not.
	General []errs.Violation `json:"general,omitempty"`
}

// Internal is the one body a 500 ever has.
//
// It carries no message, so [[D-015]]'s silence holds by construction rather
// than by a case in a switch somebody may edit: there is nowhere in this value
// for a driver's sentence to go.
func Internal() Envelope {
	return Envelope{
		Type: "error",
		Errors: Groups{
			General: []errs.Violation{{Code: errs.CodeInternal}},
		},
	}
}

// group files each violation under the rule above.
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
