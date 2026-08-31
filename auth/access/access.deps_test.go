package access

import (
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

func deps(configuration Config) *Deps { return &Deps{Config: configuration} }

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

	if f.Violations[0].Params["min"] != 10 {
		t.Fatalf("params = %v, want the configured minimum", f.Violations[0].Params)
	}
}

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

func TestThePasswordLengthIsCountedInCharacters(t *testing.T) {
	d := deps(Config{Password: PasswordConfig{MinLength: 10}})
	if err := d.checkPassword("паролище"); err == nil {
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
