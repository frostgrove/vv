package porthttp

import (
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func resolved(t *testing.T, body string, path errs.Path) errs.Violation {
	t.Helper()
	f := errs.Conflict().Code(errs.CodeUnique).
		At(path).Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	ctx := WithBody(context.Background(), []byte(body))
	_, _, env := renderCtx(t, ctx, f)
	vs := append(append([]errs.Violation{}, env.Errors.Validation...), env.Errors.General...)
	if len(vs) != 1 {
		t.Fatalf("one violation rendered as %d", len(vs))
	}
	return vs[0]
}

func TestARenamedKeyIsMarkedApproximateRatherThanGuessed(t *testing.T) {
	got := resolved(t, `{"emailAddress":"a@b.c","name":"bolt"}`, errs.Path{errs.Named("Email")})
	if !got.Approximate {
		t.Fatalf("a renamed key resolved to %s and was not marked approximate", got.Path)
	}
	if got.Path.String() != "Email" {
		t.Fatalf("the path became %s; nothing translated it, so it must stay the model field name", got.Path)
	}

	got = resolved(t, `{"email":"a@b.c","name":"bolt"}`, errs.Path{errs.Named("Email")})
	if got.Approximate {
		t.Fatalf("a key the client did send was marked approximate: %s", got.Path)
	}
	if got.Path.String() != "email" {
		t.Fatalf("the path is %s, want the key the client sent", got.Path)
	}

	got = resolved(t, `{"user":{"email":"a@b.c"}}`, errs.Path{errs.Named("Email")})
	if got.Approximate || got.Path.String() != "user.email" {
		t.Fatalf("a nested key resolved to %s (approximate=%v), want user.email", got.Path, got.Approximate)
	}

	got = resolved(t, `{"email":"a@b.c","user":{"email":"d@e.f"}}`, errs.Path{errs.Named("Email")})
	if !got.Approximate {
		t.Fatalf("an ambiguous key resolved to %s rather than declining", got.Path)
	}
	if got.Path.String() != "Email" {
		t.Fatalf("an ambiguous key shipped the guess %s", got.Path)
	}
}

func TestTheFallbackFoldsTheClientsSpelling(t *testing.T) {
	for _, key := range []string{"orgId", "org_id", "OrgID", "ORG_ID"} {
		got := resolved(t, `{"`+key+`":42}`, errs.Path{errs.Named("OrgID")})
		if got.Approximate || got.Path.String() != key {
			t.Fatalf("%q resolved to %s (approximate=%v)", key, got.Path, got.Approximate)
		}
	}

	if got := resolved(t, `{"orgName":"acme"}`, errs.Path{errs.Named("OrgID")}); !got.Approximate {
		t.Fatalf("orgName matched OrgID: %s", got.Path)
	}
}

func TestANonJSONBodyDegradesToTheModelFieldName(t *testing.T) {
	for _, body := range []string{"name=bolt&email=a%40b.c", `<widget><email>a@b.c</email></widget>`} {
		got := resolved(t, body, errs.Path{errs.Named("Email")})
		if !got.Approximate {
			t.Fatalf("a %q body resolved to %s rather than degrading", body, got.Path)
		}
		if got.Path.String() != "Email" {
			t.Fatalf("a non-JSON body produced the path %s", got.Path)
		}
	}

	got := resolved(t, `{"name":"bolt","email":"a@b.c"}`, errs.Path{errs.Named("Email")})
	if got.Approximate || got.Path.String() != "email" {
		t.Fatalf("the JSON twin resolved to %s (approximate=%v)", got.Path, got.Approximate)
	}
}

func TestAViolationWithNoPathIsNotApproximate(t *testing.T) {
	got := resolved(t, `{"name":"bolt"}`, nil)
	if got.Approximate {
		t.Fatal("a violation with no path was marked approximate")
	}
}

func TestAPathTheBodyAlreadyContainsIsLeftAlone(t *testing.T) {
	body := `{"email":"a@b.c","user":{"email":"d@e.f"}}`
	got := resolved(t, body, errs.Path{errs.Named("user"), errs.Named("email")})
	if got.Approximate || got.Path.String() != "user.email" {
		t.Fatalf("an already-translated path became %s (approximate=%v)", got.Path, got.Approximate)
	}

	if amb := resolved(t, body, errs.Path{errs.Named("Email")}); !amb.Approximate {
		t.Fatalf("the payload is not ambiguous after all: %s", amb.Path)
	}
}

func TestDecodeJSONKeepReturnsTheBytesItDecoded(t *testing.T) {
	var into map[string]any
	body := `{"name":"bolt"}`
	raw, err := DecodeJSONKeep(strings.NewReader(body), &into)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("the kept bytes are %q, want the body", raw)
	}
	if into["name"] != "bolt" {
		t.Fatalf("the decode did not happen: %v", into)
	}

	raw, err = DecodeJSONKeep(strings.NewReader(`{"name":`), &into)
	if err == nil {
		t.Fatal("a truncated body decoded without error")
	}
	if string(raw) != `{"name":` {
		t.Fatalf("a failed decode kept %q", raw)
	}

	raw, err = DecodeJSONKeep(strings.NewReader(""), &into)
	if err != nil || raw != nil {
		t.Fatalf("an empty body answered (%q, %v)", raw, err)
	}
}

func TestABodyOverTheCapIsNotRetained(t *testing.T) {
	big := `{"email":"` + strings.Repeat("x", MaxKeptBody) + `"}`
	if len(big) <= MaxKeptBody {
		t.Fatal("the fixture is under the cap, so this test measures nothing")
	}
	if got := KeepBody([]byte(big)); got != nil {
		t.Fatalf("a body of %d bytes was retained", len(got))
	}

	small := []byte(`{"email":"a@b.c"}`)
	kept := KeepBody(small)
	if string(kept) != string(small) {
		t.Fatalf("a body under the cap was not kept: %q", kept)
	}
	small[2] = 'X'
	if string(kept) == string(small) {
		t.Fatal("the kept body aliases the caller's array")
	}
}

func TestNoRetainedBodyLeavesThePathAlone(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("Email").Code(errs.CodeUnique).Fault()
	_, _, env := renderCtx(t, context.Background(), f)
	if got := env.Errors.Validation[0]; got.Approximate {
		t.Fatalf("a request with no body marked its violation approximate: %s", got.Path)
	}
}
