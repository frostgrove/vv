package accesshttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/frostgrove/vv/auth/access"
)

// Cookies is what a deployment has to decide before a credential may travel in
// one. Three settings, and none of them has a default this package could take
// without being wrong somewhere.
type Cookies struct {
	// Prefix is where this API is mounted — "/api/v1" for a router group of
	// that name, empty for an API at the root. It is not [Table.Prefix], which
	// separates two kinds of caller inside the API; this is the path the whole
	// surface hangs under, and the access cookie is scoped to it so the browser
	// attaches it to API calls and to nothing else. A credential sent with every
	// document, image and stylesheet is one that reaches every log, proxy and
	// error report in front of them.
	Prefix string

	// Secure keeps both cookies off plain HTTP.
	//
	// There is no default and none is derivable here: a workstation serves this
	// over http://localhost and would otherwise never receive the cookie at all,
	// and that convenience is exactly what must not travel to a deployment where
	// it means the credential goes in the clear. A consumer states it, and
	// states it somewhere their configuration can refuse the wrong value for the
	// environment.
	Secure bool

	// Domain is empty for a host-only cookie, which is what a single-origin
	// deployment wants. Setting it widens the cookie to every subdomain, so it
	// is worth saying out loud when a second service on the same domain is not
	// one you would hand this credential to.
	Domain string

	// SameSite is [SameSiteStrict] when it is empty. Strict is not too strict
	// for a single-page application: what it blocks is a request another *site*
	// initiated, and the application's own fetch calls are initiated by the page
	// it serves. A deployment whose API is on a different registrable domain
	// from its front end has to say [SameSiteNone] here, and pay for it with
	// Secure and with a CSRF defence of its own.
	SameSite SameSite
}

// A SameSite is what the browser is told about requests another site started.
type SameSite string

const (
	// SameSiteStrict withholds the cookie from every cross-site request, which
	// is what makes cookie-borne credentials safe from cross-site request
	// forgery without a token to carry around.
	SameSiteStrict SameSite = "Strict"
	// SameSiteLax additionally sends it on a top-level navigation. An API that
	// changes nothing on GET is not made unsafe by it; nothing here needs it.
	SameSiteLax SameSite = "Lax"
	// SameSiteNone sends it everywhere and requires Secure. It is the only value
	// that works across registrable domains, and the deployment that picks it
	// owns the CSRF problem it re-opens.
	SameSiteNone SameSite = "None"
)

// A Cookie is one credential on its way to a browser, in the shape all three
// bindings can write. Not http.Cookie: Fiber has a cookie type of its own and
// Gin takes seven arguments, so the neutral value is the one thing they share.
//
// HttpOnly is not a field. A credential cookie a script can read is the
// arrangement this package exists to avoid, and an option to turn it off is an
// option somebody would find.
type Cookie struct {
	Name    string
	Value   string
	Path    string
	Domain  string
	Expires time.Time
	Secure  bool
	// SameSite is never empty on a cookie this package builds.
	SameSite SameSite
}

// Clearing reports whether this cookie deletes the browser's copy rather than
// replacing it — an empty value with an expiry in the past.
func (this Cookie) Clearing() bool { return this.Value == "" }

// HTTP renders the cookie for an http.ResponseWriter, which is what two of the
// three bindings write through. Fiber has a cookie type of its own and
// translates there.
//
// HttpOnly is set here and is not read from anywhere, for the reason the field
// does not exist: a credential cookie a script can read is what this arrangement
// is for avoiding. A clearing cookie carries Max-Age as well as the expiry in
// the past — they mean the same thing, and a client that honours only one of
// them is a client that goes on presenting a credential nothing accepts.
func (this Cookie) HTTP() *http.Cookie {
	out := &http.Cookie{
		Name:     this.Name,
		Value:    this.Value,
		Path:     this.Path,
		Domain:   this.Domain,
		Expires:  this.Expires,
		HttpOnly: true,
		Secure:   this.Secure,
		SameSite: this.SameSite.HTTP(),
	}
	if this.Clearing() {
		out.MaxAge = -1
	}
	return out
}

// HTTP answers the net/http spelling of this attribute.
//
// A translation and not a cast. The two vocabularies agree on the three values
// today; a cast would keep agreeing right up to the moment one of them renamed
// something, and then disagree in a Set-Cookie header nobody reads.
func (this SameSite) HTTP() http.SameSite {
	switch this {
	case SameSiteLax:
		return http.SameSiteLaxMode
	case SameSiteNone:
		return http.SameSiteNoneMode
	default:
		return http.SameSiteStrictMode
	}
}

// Credentials is how one subject's two credentials reach a caller: which of
// them a request may ask for, and the cookies that carry what the body does not.
//
// Its zero value delivers everything in the body and refuses a request that
// asks for a cookie. That is the right answer for a deployment that never
// configured any: silently answering in the body would hand the credential to
// the page of a caller who asked for it to be kept away from one.
type Credentials struct {
	cookies  bool
	access   cookieSpec
	refresh  cookieSpec
	secure   bool
	domain   string
	sameSite SameSite
}

type cookieSpec struct{ name, path string }

// NewCredentials turns cookie delivery on for one subject's endpoints.
//
// It panics on a policy a browser would silently discard — SameSite=None
// without Secure, or a SameSite this package does not know. A process that
// starts with one sets cookies nothing ever receives, and the symptom is every
// session ending at the next request with nothing in any log to say why
// ([[D-021]]).
func NewCredentials(table Table, policy Cookies) Credentials {
	sameSite := policy.SameSite
	switch sameSite {
	case "":
		sameSite = SameSiteStrict
	case SameSiteStrict, SameSiteLax:
	case SameSiteNone:
		if !policy.Secure {
			panic("accesshttp: SameSite=None without Secure; a browser discards such a cookie, " +
				"so every credential this surface set would be dropped without a word")
		}
	default:
		panic(fmt.Sprintf("accesshttp: %q is not a SameSite; it is Strict, Lax or None", sameSite))
	}

	prefix := normalisePrefix(policy.Prefix)
	return Credentials{
		cookies: true,
		// Scoped to the API and not to the site. Everything under this prefix is
		// what the access token authorises, and nothing above it needs to see it.
		access: cookieSpec{name: table.AccessCookie(), path: orRoot(prefix)},
		// Scoped to the one endpoint that spends it, so the credential that
		// survives a reload is presented once every five minutes and not on
		// every call the application makes.
		refresh:  cookieSpec{name: table.RefreshCookie(), path: prefix + table.Path("/refresh")},
		secure:   policy.Secure,
		domain:   policy.Domain,
		sameSite: sameSite,
	}
}

// InCookies reports whether this surface delivers credentials in cookies at all.
func (this Credentials) InCookies() bool { return this.cookies }

// RefreshCookie is the name a binding reads a presented rotating credential
// from. Empty when this surface has no cookies to read.
func (this Credentials) RefreshCookie() string { return this.refresh.name }

// Answer splits a minted session between the body and the cookies.
//
// What travels in a cookie leaves the body entirely — the credential and the
// expiry both. `"refresh": ""` is a field a client has to know to ignore, and
// one that did not would post an empty credential to the rotation endpoint and
// be told its session is gone; an expiry beside a credential that is not there
// is the same mistake in a shape that still parses.
//
// **A half that went into the body clears the cookie it did not go into**, and
// that is not tidiness. A browser that signs in again asking for the body would
// otherwise keep the previous session's access cookie for the rest of its five
// minutes — and a guard reading the cookie prefers it to the header, so the page
// would hold a fresh token and go on acting as the session it just replaced. A
// stale refresh cookie is the same failure with a longer fuse: the next rotation
// would spend a lineage nobody is using.
func (this Credentials) Answer(
	response access.AuthResponse,
	delivery Delivery,
) (access.AuthResponse, []Cookie) {
	if !this.cookies {
		return response, nil
	}

	jar := make([]Cookie, 0, 2)
	if delivery.AccessInCookie() && response.Token != "" {
		jar = append(jar, this.cookie(this.access, response.Token, response.ExpiresAt))
		response.Token, response.ExpiresAt = "", time.Time{}
	} else {
		jar = append(jar, this.cookie(this.access, "", expired))
	}

	// An opaque strategy mints nothing to rotate, so there is never a refresh
	// cookie to set — and the clearing one below is what takes away whatever a
	// rotating deployment left behind before it was reconfigured.
	if delivery.RefreshInCookie() && response.Refresh != "" {
		jar = append(jar, this.cookie(this.refresh, response.Refresh, response.RefreshExpiresAt))
		response.Refresh, response.RefreshExpiresAt = "", time.Time{}
	} else {
		jar = append(jar, this.cookie(this.refresh, "", expired))
	}
	return response, jar
}

// Clear is what a sign-out writes: both cookies, emptied and expired.
//
// Clearing them is not what closes the session — the row and the deny-list are
// — but a browser left holding credentials that authorise nothing would present
// them on the next visit and get a 401 where it expects a login screen. It
// clears both whatever the session was delivered as, because the delivery is not
// recorded anywhere and a cookie nobody set is a cookie this does not create.
func (this Credentials) Clear() []Cookie {
	if !this.cookies {
		return nil
	}
	return []Cookie{
		this.cookie(this.access, "", expired),
		this.cookie(this.refresh, "", expired),
	}
}

// ClearRefresh is what a refused rotation writes.
//
// Only the rotating half: a rotation is anonymous, so the caller may well be
// holding a perfectly good access cookie from a session this one has nothing to
// do with — a second tab, or a credential that was already replaced. What is
// worth taking away is the one that just failed to rotate anything.
func (this Credentials) ClearRefresh() []Cookie {
	if !this.cookies {
		return nil
	}
	return []Cookie{this.cookie(this.refresh, "", expired)}
}

// expired is the past this package deletes a cookie with. Unix zero rather than
// "now minus something", so nothing depends on the clock agreeing.
var expired = time.Unix(0, 0)

func (this Credentials) cookie(spec cookieSpec, value string, expires time.Time) Cookie {
	return Cookie{
		Name:     spec.name,
		Value:    value,
		Path:     spec.path,
		Domain:   this.domain,
		Expires:  expires,
		Secure:   this.secure,
		SameSite: this.sameSite,
	}
}

// AccessCookie is the name the access token travels under, for a deployment
// wiring a guard that reads it — `authhttp.Cookie(table.AccessCookie())`.
//
// Derived from the subject's prefix rather than fixed, because the access
// cookie is scoped to the whole API: two kinds of caller on one host would
// otherwise both be called "access" at the same path, and the browser would
// overwrite one with the other. The refresh cookies do not collide — each is
// scoped to its own rotation endpoint — and they carry the prefix anyway, so
// the pair a caller holds is spelled the same way.
func (this Table) AccessCookie() string { return this.cookieName("access") }

// RefreshCookie is the name the rotating credential travels under.
func (this Table) RefreshCookie() string { return this.cookieName("refresh") }

func (this Table) cookieName(base string) string {
	prefix := trimSlashes(this.Prefix)
	if prefix == "" {
		return base
	}
	// A nested prefix is a legal path and not a legal cookie name: RFC 6265
	// names are tokens, and a slash is not a token character.
	return strings.ReplaceAll(prefix, "/", "_") + "_" + base
}

// normalisePrefix answers a mount prefix as a cookie path: leading slash, no
// trailing one. A path without the leading slash is one the browser resolves
// against the request's own directory, which is a cookie that arrives sometimes.
func normalisePrefix(prefix string) string {
	prefix = trimSlashes(prefix)
	if prefix == "" {
		return ""
	}
	return "/" + prefix
}

func orRoot(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
