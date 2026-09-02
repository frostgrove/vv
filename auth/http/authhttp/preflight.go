package authhttp

import (
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth"
)

const (
	HeaderOrigin = "Origin"

	HeaderRequestMethod = "Access-Control-Request-Method"

	PreflightStatus = http.StatusNoContent
)

func Preflight(method string, header func(name string) string) bool {
	if header == nil || !strings.EqualFold(method, http.MethodOptions) {
		return false
	}
	if header(HeaderOrigin) == "" || header(HeaderRequestMethod) == "" {
		return false
	}
	return header(auth.HeaderAuthorization) == ""
}
