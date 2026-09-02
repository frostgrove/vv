package accesshttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/frostgrove/vv/auth/access"
)

type Cookies struct {
	Prefix string

	Secure bool

	Domain string

	SameSite SameSite

	CrossSite CrossSite
}

type SameSite string

const (
	SameSiteStrict SameSite = "Strict"

	SameSiteLax SameSite = "Lax"

	SameSiteNone SameSite = "None"
)

type Cookie struct {
	Name    string
	Value   string
	Path    string
	Domain  string
	Expires time.Time
	Secure  bool

	SameSite SameSite
}

func (this Cookie) Clearing() bool { return this.Value == "" }

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

type Credentials struct {
	cookies   bool
	access    cookieSpec
	refresh   cookieSpec
	secure    bool
	domain    string
	sameSite  SameSite
	protector protector
}

type cookieSpec struct{ name, path string }

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

		access: cookieSpec{name: table.AccessCookie(), path: orRoot(prefix)},

		refresh:   cookieSpec{name: table.RefreshCookie(), path: prefix + table.Path("/refresh")},
		secure:    policy.Secure,
		domain:    policy.Domain,
		sameSite:  sameSite,
		protector: newProtector(policy.CrossSite),
	}
}

func (this Credentials) InCookies() bool { return this.cookies }

func (this Credentials) RefreshCookie() string { return this.refresh.name }

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

	if delivery.RefreshInCookie() && response.Refresh != "" {
		jar = append(jar, this.cookie(this.refresh, response.Refresh, response.RefreshExpiresAt))
		response.Refresh, response.RefreshExpiresAt = "", time.Time{}
	} else {
		jar = append(jar, this.cookie(this.refresh, "", expired))
	}
	return response, jar
}

func (this Credentials) Clear() []Cookie {
	if !this.cookies {
		return nil
	}
	return []Cookie{
		this.cookie(this.access, "", expired),
		this.cookie(this.refresh, "", expired),
	}
}

func (this Credentials) ClearRefresh() []Cookie {
	if !this.cookies {
		return nil
	}
	return []Cookie{this.cookie(this.refresh, "", expired)}
}

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

func (this Table) AccessCookie() string { return this.cookieName("access") }

func (this Table) RefreshCookie() string { return this.cookieName("refresh") }

func (this Table) cookieName(base string) string {
	prefix := trimSlashes(this.Prefix)
	if prefix == "" {
		return base
	}

	return strings.ReplaceAll(prefix, "/", "_") + "_" + base
}

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
