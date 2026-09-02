package accesshttp_test

import (
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth/access/http/accesshttp"
	"github.com/frostgrove/vv/errs"
)

func credentials(policy accesshttp.Cookies) accesshttp.Credentials {
	policy.Prefix = "/api"
	policy.Secure = true
	return accesshttp.NewCredentials(accesshttp.Table{}, policy)
}

func presenting(headers map[string]string) (func(string) string, func(string) string) {
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	return header.Get, func(name string) string {
		if name == "access" {
			return "the-session-cookie"
		}
		return ""
	}
}

func TestACookieBorneWriteFromAnotherSiteIsRefused(t *testing.T) {
	jar := credentials(accesshttp.Cookies{SameSite: accesshttp.SameSiteNone})

	header, cookie := presenting(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.test"})
	err := jar.Protect(http.MethodPost, header, cookie)
	if err == nil {
		t.Fatal("a page on another origin spent the session cookie and the deployment answered")
	}
	fault, isFault := errs.AsFault(err)
	if !isFault || fault.Kind != errs.KindForbidden {
		t.Fatalf("the refusal is %v; a caller cannot tell it from a failed sign-in", err)
	}

	header, cookie = presenting(map[string]string{"Sec-Fetch-Site": "same-origin"})
	if err := jar.Protect(http.MethodPost, header, cookie); err != nil {
		t.Fatalf("the deployment's own page was refused: %v", err)
	}
}

func TestNothingIsRefusedWhereThereIsNoAmbientCredentialToSpend(t *testing.T) {
	jar := credentials(accesshttp.Cookies{SameSite: accesshttp.SameSiteNone})
	fromElsewhere := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.test"}

	header, cookie := presenting(fromElsewhere)
	if err := jar.Protect(http.MethodGet, header, cookie); err != nil {
		t.Fatalf("a read was refused, and a browser sends the cookie on those whatever this answers: %v", err)
	}

	header, _ = presenting(fromElsewhere)
	if err := jar.Protect(http.MethodPost, header, func(string) string { return "" }); err != nil {
		t.Fatalf("a request that presented no cookie of ours was refused: %v", err)
	}

	withHeader := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.test",
		"Authorization": "Bearer t"}
	header, cookie = presenting(withHeader)
	if err := jar.Protect(http.MethodPost, header, cookie); err != nil {
		t.Fatalf("a request carrying its credential in a header was refused: %v", err)
	}
}

func TestAnOriginTheDeploymentNamedIsAllowedAndAWaiverTurnsTheCheckOff(t *testing.T) {
	named := credentials(accesshttp.Cookies{
		SameSite:  accesshttp.SameSiteNone,
		CrossSite: accesshttp.CrossSite{Origins: []string{"https://app.example/"}},
	})

	header, cookie := presenting(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://app.example"})
	if err := named.Protect(http.MethodPost, header, cookie); err != nil {
		t.Fatalf("the front end this deployment is built for was refused: %v", err)
	}

	header, cookie = presenting(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.test"})
	if err := named.Protect(http.MethodPost, header, cookie); err == nil {
		t.Fatal("naming one origin let every other origin in as well")
	}

	waived := credentials(accesshttp.Cookies{
		SameSite:  accesshttp.SameSiteNone,
		CrossSite: accesshttp.CrossSite{Unsafely: "a CSRF filter in front of this service does it"},
	})
	if err := waived.Protect(http.MethodPost, header, cookie); err != nil {
		t.Fatalf("a deployment that wrote down why it does not need the check was refused anyway: %v", err)
	}
}

func TestARefusalIsReachableWithoutReadingItsText(t *testing.T) {
	jar := credentials(accesshttp.Cookies{SameSite: accesshttp.SameSiteLax})
	header, cookie := presenting(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.test"})

	fault, isFault := errs.AsFault(jar.Protect(http.MethodDelete, header, cookie))
	if !isFault || fault.Code != accesshttp.CodeCrossSite {
		t.Fatalf("the refusal carries no code a client or a log can branch on: %v", fault)
	}
}
