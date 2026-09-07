package event

import "testing"

func TestComposeRendersTheFrozenKey(t *testing.T) {
	t.Run("the rendering is the one every stream was written under", func(t *testing.T) {
		for _, frozen := range []struct {
			parts []string
			key   Key
		}{
			{[]string{"acme", "A-17"}, "acme/A-17"},
			{[]string{"acme/evil", "A-17"}, "acme%2Fevil/A-17"},
			{[]string{"acme", "evil/A-17"}, "acme/evil%2FA-17"},
			{[]string{"рога", "17"}, "рога/17"},
			{[]string{"100%", "A-17"}, "100%25/A-17"},
			{[]string{"a\x00b"}, "a%00b"},
			{[]string{"a\x7fb"}, "a%7Fb"},
			{[]string{"a\xffb"}, "a%FFb"},
			{[]string{"a\u0085b"}, "a%C2%85b"},
			{[]string{"acme"}, "acme"},
			{[]string{"acme", ""}, "acme/"},
			{[]string{"acme", "A-17", "2026"}, "acme/A-17/2026"},
		} {
			if got := Compose(frozen.parts...); got != frozen.key {
				t.Fatalf("Compose%q renders %q where every stream written under this framework was keyed %q, so each of those aggregates now reads as version 0 and is written to a second time",
					frozen.parts, got, frozen.key)
			}
		}
	})

	t.Run("the one pair that renders equal is refused as empty", func(t *testing.T) {
		if Compose() != Compose("") {
			t.Fatal("no parts and one empty part render two different keys, so the rendering's own injectivity argument names a pair that does not exist")
		}
		if broken := checkText(string(Compose()), MaxKeyBytes); broken != textEmpty {
			t.Fatalf("the one key two identities share %q rather than being refused as empty, so those two identities share a history", broken)
		}
	})
}
