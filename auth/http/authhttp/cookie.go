package authhttp

import (
	"net/http"

	"github.com/frostgrove/vv/auth"
)

// Cookie reads the credential from a cookie, falling back to the Authorization
// header when the request carries none.
//
//	guard := auth.NewGuard(authn, authhttp.Cookie("access"))
//
// It is here and not in `auth` because it needs an RFC 6265 parser, and
// `net/http` has one. Package `auth` takes no HTTP dependency, so that the gRPC
// interceptor gets the transport-neutral half of a guard without one ([[D-045]],
// [[D-055]]) — and a cookie parser hand-rolled there to avoid the import would
// be six lines of string splitting maintained against a specification nobody
// would re-read.
//
// # The fallback is the point
//
// [auth.Lookup] *replaces* the credential lookup rather than adding to it, so a
// cookie option written the obvious way turns the Authorization header off — in
// the same application that wants both, because the pages' fetch calls send a
// cookie and a native client sends a header. Falling back covers that, and two
// more cases that are otherwise silent: a guard carrying this option and handed
// to `authgrpc`, where "Cookie" is a metadata key no client sends, and a browser
// whose access cookie has expired while its refresh cookie has not.
//
// The header is [auth.HeaderAuthorization] and not whatever [auth.Header] was
// given. An option cannot read another option's choice, and a guard that reads
// both a cookie and a header of its own is a lookup of its own — five lines,
// with this function's body as the shape.
//
// # What comes out
//
// A cookie carries a bare token with no scheme, and every authenticator in this
// library refuses a credential that is not Bearer. So the scheme is supplied
// here: presenting a token in a cookie means the same thing as presenting it in
// an `Authorization: Bearer` header, and an authenticator that could tell them
// apart would be one a caller could pick a verifier with.
func Cookie(name string) auth.Option {
	return auth.Lookup(func(get func(name string) string) (auth.Credential, bool) {
		if token := cookie(get("Cookie"), name); token != "" {
			return auth.Credential{Scheme: auth.SchemeBearer, Token: token}, true
		}
		return auth.ParseAuthorization(get(auth.HeaderAuthorization))
	})
}

// cookie answers one cookie's value out of a Cookie header.
//
// A header this parser rejects answers "" rather than an error: the request
// still has an Authorization header to be judged on, and a malformed Cookie
// header is not something the caller of a guard can act on.
func cookie(header, name string) string {
	if header == "" {
		return ""
	}
	parsed, err := http.ParseCookie(header)
	if err != nil {
		return ""
	}
	for _, candidate := range parsed {
		if candidate.Name == name {
			return candidate.Value
		}
	}
	return ""
}
