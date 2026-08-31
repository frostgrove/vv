package authhttp

import (
	"net/http"

	"github.com/frostgrove/vv/auth"
)

func Cookie(name string) auth.Option {
	return auth.Lookup(func(get func(name string) string) (auth.Credential, bool) {
		if token := cookie(get("Cookie"), name); token != "" {
			return auth.Credential{Scheme: auth.SchemeBearer, Token: token}, true
		}
		return auth.ParseAuthorization(get(auth.HeaderAuthorization))
	})
}

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
