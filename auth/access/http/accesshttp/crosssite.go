package accesshttp

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/errs"
)

type CrossSite struct {
	Origins []string `yaml:"origins"`

	Unsafely string `yaml:"unsafely"`
}

const (
	HeaderOrigin = "Origin"

	HeaderFetchSite = "Sec-Fetch-Site"
)

const CodeCrossSite errs.Code = "cross_site_request"

type protector struct {
	on      bool
	origins map[string]struct{}
}

func newProtector(policy CrossSite) protector {
	if strings.TrimSpace(policy.Unsafely) != "" {
		return protector{}
	}
	origins := make(map[string]struct{}, len(policy.Origins))
	for _, origin := range policy.Origins {
		normalised := strings.ToLower(strings.TrimRight(strings.TrimSpace(origin), "/"))
		if normalised == "" {
			continue
		}
		origins[normalised] = struct{}{}
	}
	return protector{on: true, origins: origins}
}

// A cookie is ambient authority, a header is not: this refuses a request that
// would spend a credential the caller never had to present. Hence all three
// conditions — an unsafe method, our own cookie on the request, no header
// credential — and hence nothing here decides who the caller is.
func (this Credentials) Protect(
	method string,
	header func(name string) string,
	cookie func(name string) string,
) error {
	if !this.cookies || !this.protector.on || header == nil {
		return nil
	}
	if safeMethod(method) {
		return nil
	}
	if header(auth.HeaderAuthorization) != "" {
		return nil
	}
	if !this.presented(cookie) {
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(header(HeaderFetchSite))) {
	case "same-origin", "none":
		return nil
	}
	origin := header(HeaderOrigin)
	if origin != "" && this.protector.allows(origin) {
		return nil
	}
	return crossSite(origin)
}

func (this Credentials) presented(cookie func(name string) string) bool {
	if cookie == nil {
		return false
	}
	return cookie(this.access.name) != "" || cookie(this.refresh.name) != ""
}

func (this protector) allows(origin string) bool {
	_, allowed := this.origins[strings.ToLower(strings.TrimRight(strings.TrimSpace(origin), "/"))]
	return allowed
}

func safeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func crossSite(origin string) error {
	return errs.Forbidden().
		Code(CodeCrossSite).
		Message("this request did not come from a page this deployment serves").
		Entity("Session").Op("access.cross-site").
		Wrapping(fmt.Errorf("accesshttp: a cookie-borne credential was spent from origin %q", origin)).
		Fault()
}
