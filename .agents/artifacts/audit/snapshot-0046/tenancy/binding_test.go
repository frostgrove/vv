package tenancy

import (
	"bytes"
	"testing"
)

// Two authorities never share a salt, so from outside the package the salt alone
// refuses a foreign scope and an Origin that had left the binding would look
// exactly the same. Isolating it needs one salt and two origins, which only this
// package can arrange — and Origin is the half that survives a deployment cloned
// from another's configuration, where the salt is regenerated but the wiring is
// copied verbatim.
func TestTheOriginIsPartOfTheBindingAndNotJustTheSalt(t *testing.T) {
	salt := []byte("one salt shared by both deployments!!")
	resolution := Resolution{
		Reference: Reference{value: "acme"},
		Lifecycle: Active,
		Epoch:     Epoch(1),
	}

	staging := bind(salt, "invoices.staging", resolution)
	production := bind(salt, "invoices.production", resolution)
	if bytes.Equal(staging[:], production[:]) {
		t.Fatal("two deployments sharing a salt mint interchangeable scopes — Origin is not in the binding")
	}
	if again := bind(salt, "invoices.staging", resolution); !bytes.Equal(staging[:], again[:]) {
		t.Fatal("the same inputs minted two different bindings, so nothing here is comparable")
	}
}

func TestEveryFieldOfAResolutionIsPartOfItsBinding(t *testing.T) {
	salt := []byte("one salt shared by both deployments!!")
	base := Resolution{Reference: Reference{value: "acme"}, Lifecycle: Active, Epoch: Epoch(1)}
	digest := bind(salt, "invoices", base)

	for name, changed := range map[string]Resolution{
		"another tenant":     {Reference: Reference{value: "globex"}, Lifecycle: Active, Epoch: Epoch(1)},
		"another lifecycle":  {Reference: Reference{value: "acme"}, Lifecycle: Suspended, Epoch: Epoch(1)},
		"another generation": {Reference: Reference{value: "acme"}, Lifecycle: Active, Epoch: Epoch(2)},
	} {
		t.Run(name, func(t *testing.T) {
			other := bind(salt, "invoices", changed)
			if bytes.Equal(digest[:], other[:]) {
				t.Fatal("this field is outside the binding, so a scope carrying it can be edited without the authority noticing")
			}
		})
	}
}

// The reference is length-prefixed, so two tenants whose names concatenate the
// same way are still two tenants.
func TestFieldsCannotBeSlidPastTheLengthPrefix(t *testing.T) {
	salt := []byte("one salt shared by both deployments!!")
	first := bind(salt, "invoices", Resolution{Reference: Reference{value: "ab"}, Lifecycle: Active, Epoch: Epoch(1)})
	second := bind(salt, "invoicesa", Resolution{Reference: Reference{value: "b"}, Lifecycle: Active, Epoch: Epoch(1)})
	if bytes.Equal(first[:], second[:]) {
		t.Fatal("origin and reference run together in the digest, so one tenant can be spelled as another")
	}
}
