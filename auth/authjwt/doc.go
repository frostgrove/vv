// Package authjwt verifies a JSON Web Token and turns it into a caller.
//
// The parser is generic over the claims: give it your own struct and it is the
// only thing you ever see.
//
//	type MyClaims struct {
//	    Subject string `json:"sub"`
//	    Tenant  int64  `json:"tenant"`
//	    Scope   string `json:"scope"`
//	}
//
//	parser := authjwt.New[MyClaims](
//	    authjwt.HMAC(secret),
//	    authjwt.Issuer("https://id.example.com"),
//	    authjwt.Audience("articles-api"),
//	)
//
//	claims, err := parser.Parse(ctx, token)   // MyClaims, and nothing of ours
//
// Nothing above mentions [auth.Principal]. A consumer who wants a JWT parser
// and none of the rest of this library stops here — that is the whole reason
// the parser and the bridge are two calls rather than one.
//
// The bridge is the second call:
//
//	authn := authjwt.Authenticator(parser, func(_ context.Context, c MyClaims) (auth.Principal, error) {
//	    return auth.Claims{
//	        Sub:         c.Subject,
//	        Permissions: auth.Scopes(c.Scope),
//	        Attrs:       map[string]any{"tenant": c.Tenant},
//	    }, nil
//	})
//
// and [Standard] is both calls for the ordinary shape of token, where [Claims]
// is the struct and no mapping is needed.
//
// # What is checked, and what a caller cannot turn off
//
// The signing algorithm is pinned by the key, not read from the token. A
// [KeySource] carries the methods it can verify, and a token whose alg is
// anything else is refused before a key is consulted. That closes both classic
// forgeries at once: alg=none, and an RSA public key presented as an HMAC
// secret so that anything able to read the public key can mint tokens.
//
// exp is required. A token with no expiry is a credential that never stops
// working, and a library that treats the claim as optional turns one leaked
// token into a permanent one. [AllowNoExpiry] exists for a token issued by
// something that genuinely cannot set it, and naming it is the point.
//
// iss and aud are required in the same sense — [New] refuses to build a parser
// that checks neither, and [AllowAnyIssuer] and [AllowAnyAudience] are how a
// deployment says so out loud. An unaudienced token is replayable against every
// other service that trusts the same issuer, which is a failure nobody notices
// until one of those services is compromised. The refusal is a panic at
// construction, so it is a process that does not start rather than a request
// that is quietly over-trusted ([[D-021]]).
//
// # What it does not do
//
// It does not issue tokens. There is no signer here, no refresh, no key
// rotation on the issuing side; this package reads what was presented.
//
// It does not decide anything. What a claim means and what a permission grants
// is crud/decorators/security's ([[D-055]]).
package authjwt
