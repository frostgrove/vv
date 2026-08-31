package porthttp

import "github.com/frostgrove/vv/errs"

type Envelope struct {
	Type string `json:"type"`

	Partial bool   `json:"partial,omitempty"`
	Errors  Groups `json:"errors"`
}

type Groups struct {
	Validation []errs.Violation `json:"validation,omitempty"`

	General []errs.Violation `json:"general,omitempty"`
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
