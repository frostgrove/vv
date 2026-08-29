package accesshttp

import (
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

// DeliveryHeader is where a caller says which of its two credentials it wants
// to be able to read.
//
// A header rather than a field in the body, and the sign-up endpoint is what
// decides it: that body is the application's own type ([[D-066]]), so there is
// no field this package could add to it without wrapping it — and the wrapper
// would have to be written again for every payload, by every consumer. A header
// is the same two lines on all three endpoints and in all three bindings, and
// it is also the only thing a rotation *has*: a browser rotating by cookie
// sends no body at all.
//
// It is a request header a browser does not send by itself, so a deployment
// serving a page from another origin has to name it in
// Access-Control-Allow-Headers. A preflight that does not allow it drops the
// header rather than the request, and the sign-in then answers with the safe
// default below instead of what the page asked for.
const DeliveryHeader = "X-Auth-Delivery"

// A Delivery says where the two credentials a sign-in mints are written.
//
// There are three and not four. The missing combination — the access token in a
// cookie and the rotating credential in the body — puts the durable half where
// a script can read it and the short-lived half where it cannot, which is the
// arrangement backwards.
type Delivery string

const (
	// DeliverCookies is both credentials in HttpOnly cookies, and a body that
	// carries neither. It is what a browser wants: a page that cannot read
	// either credential cannot leak one, and a cross-site scripting bug buys an
	// attacker what they can do while the page is open rather than a credential
	// they can take away.
	DeliverCookies Delivery = "cookies"

	// DeliverRefreshCookie is the rotating credential in a cookie and the access
	// token in the body. It is for a page that sends its own Authorization
	// header — an event stream that cannot, or a second API on another site that
	// no cookie reaches, are the reasons to keep the token in a variable — while
	// what survives a reload stays unreadable.
	DeliverRefreshCookie Delivery = "refresh-cookie"

	// DeliverBody is both credentials in the body and no cookie at all. It is
	// what a mobile client or a command-line tool wants: there is no browser to
	// attach a cookie, and the platform keychain is a better place for the
	// rotating credential than anything this API could set.
	DeliverBody Delivery = "body"
)

// AccessInCookie reports whether the access token goes into a cookie.
func (this Delivery) AccessInCookie() bool { return this == DeliverCookies }

// RefreshInCookie reports whether the rotating credential goes into a cookie.
func (this Delivery) RefreshInCookie() bool {
	return this == DeliverCookies || this == DeliverRefreshCookie
}

// Requested answers the delivery this request asked for.
//
// get reads a request header by name and answers "" for one that is not there,
// which is the shape every binding has already ([[D-045]]).
//
// Two rules, and both are about failing in the direction that is safe:
//
//   - **Silence takes the most closed delivery this deployment offers.** Where
//     cookies are configured that is both of them, so a browser that says
//     nothing is still a browser that cannot read its own credentials; a client
//     that wanted the body and forgot to ask finds its session unusable at once,
//     which is a loud failure rather than a quiet downgrade.
//   - **A value this package does not know is refused.** A typo that fell back
//     to the default would be a client asking for the body, being handed
//     cookies, and reporting that sign-in "returns nothing".
//
// The refusal is `invalid_enum`, which the standard vocabulary classifies as a
// validation failure and a transport renders as 422 rather than 400. That is
// what it is: the request is well formed and one of its values is not one of
// the allowed ones.
func (this Credentials) Requested(get func(name string) string) (Delivery, error) {
	if get == nil {
		return this.Default(), nil
	}
	switch requested := Delivery(get(DeliveryHeader)); requested {
	case "":
		return this.Default(), nil
	case DeliverBody:
		return DeliverBody, nil
	case DeliverCookies, DeliverRefreshCookie:
		if !this.cookies {
			// Answering with the body instead would hand the credential to the
			// page of a caller that asked for it to be kept away from one.
			return "", porthttp.BadRequestAs(errs.CodeInvalidEnum, port.At(),
				"%s: %q is not available here, because this deployment delivers credentials in the body",
				DeliveryHeader, requested)
		}
		return requested, nil
	default:
		return "", porthttp.BadRequestAs(errs.CodeInvalidEnum, port.At(),
			"%s: %q is not a delivery; it is one of %q, %q or %q",
			DeliveryHeader, requested, DeliverCookies, DeliverRefreshCookie, DeliverBody)
	}
}

// Default is what a request that named no delivery gets.
func (this Credentials) Default() Delivery {
	if this.cookies {
		return DeliverCookies
	}
	return DeliverBody
}

// Rotating answers where a rotation's credentials go.
//
// The rotating half is forced to the channel the presented credential arrived
// on, and the caller does not get a say. That is the whole reason this function
// exists: a cookie travels automatically, so a script injected into the page can
// call the rotation endpoint whether or not it can read anything — and if the
// endpoint honoured "give it back in the body", the script would ask, and walk
// away with a credential good for weeks from its own machine. The cookie, whose
// only purpose is that a script cannot read it, would have bought nothing.
//
// What the request still decides is the access token, which is the difference
// between [DeliverCookies] and [DeliverRefreshCookie] for a browser. A
// credential presented in the body answers wholly in the body: that caller has
// no browser holding a cookie, so there is nothing for one to be worth.
func Rotating(requested Delivery, byCookie bool) Delivery {
	if !byCookie {
		return DeliverBody
	}
	if requested.AccessInCookie() {
		return DeliverCookies
	}
	return DeliverRefreshCookie
}
