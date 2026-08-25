package crudhttp

import (
	"context"
	"strings"
	"testing"

	"github.com/shardit-io/vv/errs"
)

// resolved renders one violation at path through the raw-body fallback and
// reports where it landed and whether the renderer marked it approximate.
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

// [[D-043]]'s owed control: a key the client renamed, with no mapper entry to
// translate it, produces the approximate marker rather than a wrong path.
//
// Without it, a fallback that always guessed the first matching key would pass
// every other test here.
func TestARenamedKeyIsMarkedApproximateRatherThanGuessed(t *testing.T) {
	got := resolved(t, `{"emailAddress":"a@b.c","name":"bolt"}`, errs.Path{errs.Named("Email")})
	if !got.Approximate {
		t.Fatalf("a renamed key resolved to %s and was not marked approximate", got.Path)
	}
	if got.Path.String() != "Email" {
		t.Fatalf("the path became %s; nothing translated it, so it must stay the model field name", got.Path)
	}

	// The first control: the same body with the key present resolves exactly
	// and is *not* approximate. Without it this passes for a resolver that
	// marks everything approximate and translates nothing.
	got = resolved(t, `{"email":"a@b.c","name":"bolt"}`, errs.Path{errs.Named("Email")})
	if got.Approximate {
		t.Fatalf("a key the client did send was marked approximate: %s", got.Path)
	}
	if got.Path.String() != "email" {
		t.Fatalf("the path is %s, want the key the client sent", got.Path)
	}

	// The second control: nesting. The client's path is what is wanted, not the
	// leaf name on its own.
	got = resolved(t, `{"user":{"email":"a@b.c"}}`, errs.Path{errs.Named("Email")})
	if got.Approximate || got.Path.String() != "user.email" {
		t.Fatalf("a nested key resolved to %s (approximate=%v), want user.email", got.Path, got.Approximate)
	}

	// The third control: a repeated key at two nestings is two candidates, and
	// this layer does not pick. Value matching is what would separate them and
	// nothing fills errs.Detail.Value yet, so the honest answer is approximate.
	got = resolved(t, `{"email":"a@b.c","user":{"email":"d@e.f"}}`, errs.Path{errs.Named("Email")})
	if !got.Approximate {
		t.Fatalf("an ambiguous key resolved to %s rather than declining", got.Path)
	}
	if got.Path.String() != "Email" {
		t.Fatalf("an ambiguous key shipped the guess %s", got.Path)
	}
}

// The client's spelling is what is matched, not the model's: a field the model
// calls OrgID reaches a body that sent orgId, org_id or ORG_ID.
func TestTheFallbackFoldsTheClientsSpelling(t *testing.T) {
	for _, key := range []string{"orgId", "org_id", "OrgID", "ORG_ID"} {
		got := resolved(t, `{"`+key+`":42}`, errs.Path{errs.Named("OrgID")})
		if got.Approximate || got.Path.String() != key {
			t.Fatalf("%q resolved to %s (approximate=%v)", key, got.Path, got.Approximate)
		}
	}

	// The control: folding is not "anything goes". A different field is still a
	// miss.
	if got := resolved(t, `{"orgName":"acme"}`, errs.Path{errs.Named("OrgID")}); !got.Approximate {
		t.Fatalf("orgName matched OrgID: %s", got.Path)
	}
}

// [[D-043]]'s other owed item. Fiber's binder dispatches on Content-Type and
// still accepts XML and form encodings, and a form body has no nesting to
// index — so the path degrades to the model field name and says so.
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

	// The control: the same violation with a JSON body of the same content is
	// resolved and exact, so "degrades" is measuring the encoding and not the
	// resolver being broken.
	got := resolved(t, `{"name":"bolt","email":"a@b.c"}`, errs.Path{errs.Named("Email")})
	if got.Approximate || got.Path.String() != "email" {
		t.Fatalf("the JSON twin resolved to %s (approximate=%v)", got.Path, got.Approximate)
	}
}

// A violation with no field to translate is a complete answer, not an
// unresolved one — a composite key at its common ancestor, a bare 404. Marking
// it approximate would tell a client the library failed at something it never
// attempted.
func TestAViolationWithNoPathIsNotApproximate(t *testing.T) {
	got := resolved(t, `{"name":"bolt"}`, nil)
	if got.Approximate {
		t.Fatal("a violation with no path was marked approximate")
	}
}

// A path that is already the client's own — a validation bridge's, a query
// document's — is left exactly as it is, even when the same leaf name appears
// elsewhere in the payload.
func TestAPathTheBodyAlreadyContainsIsLeftAlone(t *testing.T) {
	body := `{"email":"a@b.c","user":{"email":"d@e.f"}}`
	got := resolved(t, body, errs.Path{errs.Named("user"), errs.Named("email")})
	if got.Approximate || got.Path.String() != "user.email" {
		t.Fatalf("an already-translated path became %s (approximate=%v)", got.Path, got.Approximate)
	}

	// The control: the ambiguity is real, so the same leaf reached by its name
	// alone still declines. Without it, this passes for a resolver that
	// resolves everything.
	if amb := resolved(t, body, errs.Path{errs.Named("Email")}); !amb.Approximate {
		t.Fatalf("the payload is not ambiguous after all: %s", amb.Path)
	}
}

// ---------------------------------------------------------------------------
// the carrier

// The bytes are the caller's to keep, and they are a copy: Fiber documents
// c.Body() as valid only within the handler.
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

	// A body that does not decode is still kept, because a malformed payload is
	// exactly when a client most needs to be told which part of it was wrong.
	raw, err = DecodeJSONKeep(strings.NewReader(`{"name":`), &into)
	if err == nil {
		t.Fatal("a truncated body decoded without error")
	}
	if string(raw) != `{"name":` {
		t.Fatalf("a failed decode kept %q", raw)
	}

	// The control: an empty body is not an error and keeps nothing. POST /count
	// with no body means "no narrowing".
	raw, err = DecodeJSONKeep(strings.NewReader(""), &into)
	if err != nil || raw != nil {
		t.Fatalf("an empty body answered (%q, %v)", raw, err)
	}
}

// Holding a megabyte of request per in-flight write so a hypothetical error can
// name a field is the wrong trade. Past the cap nothing is kept, and the path
// is then approximate rather than wrong.
func TestABodyOverTheCapIsNotRetained(t *testing.T) {
	big := `{"email":"` + strings.Repeat("x", MaxKeptBody) + `"}`
	if len(big) <= MaxKeptBody {
		t.Fatal("the fixture is under the cap, so this test measures nothing")
	}
	if got := KeepBody([]byte(big)); got != nil {
		t.Fatalf("a body of %d bytes was retained", len(got))
	}

	// The control: one byte under the cap is kept, and it is a copy rather than
	// the caller's own array — a stored reference is the use-after-free
	// [[D-043]] names.
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

// No body retained is not the same as a body that could not be read. Nothing to
// translate is not a failure to translate, so a GET's violation is not marked
// approximate for having no payload.
func TestNoRetainedBodyLeavesThePathAlone(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("Email").Code(errs.CodeUnique).Fault()
	_, _, env := renderCtx(t, context.Background(), f)
	if got := env.Errors.Validation[0]; got.Approximate {
		t.Fatalf("a request with no body marked its violation approximate: %s", got.Path)
	}
}
