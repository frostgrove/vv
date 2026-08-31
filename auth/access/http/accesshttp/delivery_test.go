package accesshttp

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/port"
)

func headers(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

func TestARequestThatNamesNoDeliveryGetsTheMostClosedOneAvailable(t *testing.T) {
	withCookies := NewCredentials(Table{}, Cookies{Prefix: "/api/v1"})
	got, err := withCookies.Requested(headers(nil))
	if err != nil {
		t.Fatalf("a request with no delivery header was refused: %v", err)
	}
	if got != DeliverCookies {
		t.Fatalf("silence took %q, want %q", got, DeliverCookies)
	}

	got, err = Credentials{}.Requested(headers(nil))
	if err != nil {
		t.Fatalf("a body-only surface refused a request with no delivery header: %v", err)
	}
	if got != DeliverBody {
		t.Fatalf("silence on a body-only surface took %q, want %q", got, DeliverBody)
	}
}

func TestADeliveryNobodyDefinedIsRefusedRatherThanDefaulted(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{})
	_, err := credentials.Requested(headers(map[string]string{DeliveryHeader: "cookie"}))
	if err == nil {
		t.Fatal("a delivery this package does not define was accepted")
	}

	if !errors.Is(err, port.ErrBadRequest) {
		t.Fatalf("the refusal is not a client mistake: %v", err)
	}

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

	if _, err := (Credentials{}).Requested(headers(map[string]string{DeliveryHeader: string(DeliverBody)})); err != nil {
		t.Fatalf("a body-only surface refused the body: %v", err)
	}
}

func TestRotationAnswersThroughTheChannelTheCredentialArrivedOn(t *testing.T) {
	if got := Rotating(DeliverBody, true); got.RefreshInCookie() != true {
		t.Fatal("a cookie-borne rotation answered the credential into the body because the request asked")
	}
	if got := Rotating(DeliverCookies, false); got.RefreshInCookie() != false {
		t.Fatal("a body-borne rotation put the credential into a cookie the caller has no browser for")
	}

	if got := Rotating(DeliverCookies, true); got != DeliverCookies {
		t.Fatalf("rotating for a browser that keeps both in cookies answered %q", got)
	}
	if got := Rotating(DeliverRefreshCookie, true); got != DeliverRefreshCookie {
		t.Fatalf("rotating for a browser that reads its own token answered %q", got)
	}
}

func TestNoDeliveryPutsTheAccessTokenInACookieAndTheRotatingOneInTheBody(t *testing.T) {
	for _, delivery := range []Delivery{DeliverBody, DeliverRefreshCookie, DeliverCookies} {
		if delivery.AccessInCookie() && !delivery.RefreshInCookie() {
			t.Fatalf("%q keeps the short-lived credential from the page and hands over the durable one", delivery)
		}
	}
}
