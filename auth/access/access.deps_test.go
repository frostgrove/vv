package access

import (
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

func deps(configuration Config) *Deps { return &Deps{Config: configuration} }

// Length and nothing else. A composition rule ("one digit, one symbol") makes
// the password people choose more predictable, not less.
func TestAPasswordIsRefusedOnLengthAndNothingElse(t *testing.T) {
	d := deps(Config{Password: PasswordConfig{MinLength: 10}})

	if err := d.checkPassword("0123456789"); err != nil {
		t.Fatalf("a password exactly at the minimum was refused: %v", err)
	}
	if err := d.checkPassword("all lowercase letters and no digits at all"); err != nil {
		t.Fatalf("a long passphrase with no digits or symbols was refused: %v", err)
	}

	err := d.checkPassword("012345678")
	if err == nil {
		t.Fatal("a password one character short was accepted")
	}
	f, ok := errs.AsFault(err)
	if !ok || len(f.Violations) != 1 {
		t.Fatalf("the refusal is %v, which no transport can turn into a 422 naming a field", err)
	}
	if f.Violations[0].Code != CodeWeakPassword {
		t.Fatalf("error_code = %q, want %q", f.Violations[0].Code, CodeWeakPassword)
	}
	if len(f.Violations[0].Path) != 1 || f.Violations[0].Path[0].Name != "Password" {
		t.Fatalf("the refusal does not name the password field: %v", f.Violations[0].Path)
	}
	// The minimum travels with the violation, so a message catalogue can say
	// what it is without this package writing the sentence.
	if f.Violations[0].Params["min"] != 10 {
		t.Fatalf("params = %v, want the configured minimum", f.Violations[0].Params)
	}
}

// A length of zero is "not configured", not "no rule". A deployment that left
// the key out must not accept a one-character password.
func TestAnUnsetMinimumFallsBackRatherThanAcceptingAnything(t *testing.T) {
	d := deps(Config{})

	if err := d.checkPassword("x"); err == nil {
		t.Fatal("a one-character password was accepted because nobody configured a minimum")
	}
	short := make([]byte, DefaultMinPasswordLength)
	for i := range short {
		short[i] = 'x'
	}
	if err := d.checkPassword(string(short)); err != nil {
		t.Fatalf("a password at the default minimum was refused: %v", err)
	}
}

// A password is counted in characters and not in bytes: "пароль" is six
// characters and twelve bytes, and a rule that counted bytes would let a
// six-character Cyrillic password through a ten-character minimum.
func TestThePasswordLengthIsCountedInCharacters(t *testing.T) {
	d := deps(Config{Password: PasswordConfig{MinLength: 10}})
	if err := d.checkPassword("паролище"); err == nil { // 8 characters, 16 bytes
		t.Fatal("an eight-character password passed a ten-character minimum by being counted in bytes")
	}
}

func TestAnUnsetSessionLifetimeFallsBackToTheDefault(t *testing.T) {
	if got := deps(Config{}).Config.Sessions().TTL; got != DefaultSessionTTL {
		t.Fatalf("session TTL = %v, want %v", got, DefaultSessionTTL)
	}
	configured := Config{Session: SessionConfig{TTL: 2 * time.Hour}}
	if got := deps(configured).Config.Sessions().TTL; got != 2*time.Hour {
		t.Fatalf("session TTL = %v, want the configured value", got)
	}
}

// Every failed sign-in gets one answer. Telling an unknown address apart from a
// wrong password turns the endpoint into a way to ask whether somebody has an
// account here.
func TestARefusedSignInSaysNothingAboutWhichHalfWasWrong(t *testing.T) {
	err := badCredentials("Login")
	f, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("badCredentials returned %v", err)
	}
	if f.Kind != errs.KindUnauthorized {
		t.Fatalf("kind = %v, want unauthorized", f.Kind)
	}
	if f.Code != CodeBadCredentials {
		t.Fatalf("code = %q", f.Code)
	}
	// The message may name both halves — it has to, to be useful — but never
	// one of them. Each phrase below identifies a single branch, which is the
	// thing a stranger is trying to learn.
	for _, giveaway := range []string{
		"no such", "not found", "unknown", "does not exist", "no account",
		"wrong password", "incorrect password", "is disabled", "deactivated",
	} {
		if containsFold(f.Message, giveaway) {
			t.Errorf("the message says %q, which tells a stranger which half was wrong: %q", giveaway, f.Message)
		}
	}
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
