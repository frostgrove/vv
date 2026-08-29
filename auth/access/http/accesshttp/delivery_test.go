package accesshttp

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/port"
)

// headers is a request's headers in the shape every binding hands a lookup.
func headers(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// A browser that says nothing must not be quietly downgraded to the delivery it
// can read. The other direction is a loud failure — a native client that forgot
// to ask finds an empty body and no session — and loud is the one to prefer.
func TestARequestThatNamesNoDeliveryGetsTheMostClosedOneAvailable(t *testing.T) {
	withCookies := NewCredentials(Table{}, Cookies{Prefix: "/api/v1"})
	got, err := withCookies.Requested(headers(nil))
	if err != nil {
		t.Fatalf("a request with no delivery header was refused: %v", err)
	}
	if got != DeliverCookies {
		t.Fatalf("silence took %q, want %q", got, DeliverCookies)
	}

	// The control: with no cookies configured there is nothing more closed than
	// the body, and silence takes that rather than refusing.
	got, err = Credentials{}.Requested(headers(nil))
	if err != nil {
		t.Fatalf("a body-only surface refused a request with no delivery header: %v", err)
	}
	if got != DeliverBody {
		t.Fatalf("silence on a body-only surface took %q, want %q", got, DeliverBody)
	}
}

// A typo must not fall back to the default. A client that asked for the body,
// was handed cookies and reported that sign-in "returns nothing" is the failure
// this refusal replaces.
func TestADeliveryNobodyDefinedIsRefusedRatherThanDefaulted(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{})
	_, err := credentials.Requested(headers(map[string]string{DeliveryHeader: "cookie"}))
	if err == nil {
		t.Fatal("a delivery this package does not define was accepted")
	}
	// Classified as the caller's mistake. Without the sentinel it renders as a
	// 500, and an outage is what a client retries.
	if !errors.Is(err, port.ErrBadRequest) {
		t.Fatalf("the refusal is not a client mistake: %v", err)
	}
	// The three that do exist all pass, or the refusal above proves nothing.
	for _, delivery := range []Delivery{DeliverBody, DeliverRefreshCookie, DeliverCookies} {
		got, err := credentials.Requested(headers(map[string]string{DeliveryHeader: string(delivery)}))
		if err != nil {
			t.Fatalf("%q was refused: %v", delivery, err)
		}
		if got != delivery {
			t.Fatalf("%q was read as %q", delivery, got)
		}
	}
}

// Answering in the body instead would hand the credential to the page of a
// caller who asked for it to be kept away from one.
func TestAskingForACookieWhereNoneAreConfiguredIsRefused(t *testing.T) {
	for _, delivery := range []Delivery{DeliverCookies, DeliverRefreshCookie} {
		_, err := Credentials{}.Requested(headers(map[string]string{DeliveryHeader: string(delivery)}))
		if err == nil {
			t.Fatalf("a body-only surface accepted %q", delivery)
		}
		if !errors.Is(err, port.ErrBadRequest) {
			t.Fatalf("the refusal of %q is not a client mistake: %v", delivery, err)
		}
	}
	// The control: the same surface answers the delivery it does have.
	if _, err := (Credentials{}).Requested(headers(map[string]string{DeliveryHeader: string(DeliverBody)})); err != nil {
		t.Fatalf("a body-only surface refused the body: %v", err)
	}
}

// The rule the rotation endpoint exists for. A script on the page cannot read
// the cookie but can make the browser send it, so an endpoint that honoured
// "give it back in the body" would hand that script a credential good for weeks
// from its own machine.
func TestRotationAnswersThroughTheChannelTheCredentialArrivedOn(t *testing.T) {
	if got := Rotating(DeliverBody, true); got.RefreshInCookie() != true {
		t.Fatal("a cookie-borne rotation answered the credential into the body because the request asked")
	}
	if got := Rotating(DeliverCookies, false); got.RefreshInCookie() != false {
		t.Fatal("a body-borne rotation put the credential into a cookie the caller has no browser for")
	}

	// What the request does still decide is the access token, which is the
	// difference between the two cookie deliveries for a browser.
	if got := Rotating(DeliverCookies, true); got != DeliverCookies {
		t.Fatalf("rotating for a browser that keeps both in cookies answered %q", got)
	}
	if got := Rotating(DeliverRefreshCookie, true); got != DeliverRefreshCookie {
		t.Fatalf("rotating for a browser that reads its own token answered %q", got)
	}
}

// The fourth combination is the arrangement backwards: the durable credential
// where a script can read it, the five-minute one where it cannot.
func TestNoDeliveryPutsTheAccessTokenInACookieAndTheRotatingOneInTheBody(t *testing.T) {
	for _, delivery := range []Delivery{DeliverBody, DeliverRefreshCookie, DeliverCookies} {
		if delivery.AccessInCookie() && !delivery.RefreshInCookie() {
			t.Fatalf("%q keeps the short-lived credential from the page and hands over the durable one", delivery)
		}
	}
}
