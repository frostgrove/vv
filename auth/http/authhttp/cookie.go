package authhttp

import (
	"net/http"

	"github.com/frostgrove/vv/auth"
)

func Cookie(name string) auth.Option {
	return auth.LookupOrRefuse(func(get func(name string) string) (auth.Credential, bool, error) {
		token, count := cookie(get("Cookie"), name)
		header, presented := auth.ParseAuthorization(get(auth.HeaderAuthorization))
		switch {
		case count > 1:
			return auth.Credential{}, false, auth.AmbiguousCredential(
				"the request carries more than one " + name + " cookie")
		case count == 1 && presented:
			return auth.Credential{}, false, auth.AmbiguousCredential(
				"the request carries both a " + name + " cookie and an Authorization header")
		case count == 1:
			return auth.Credential{Scheme: auth.SchemeBearer, Token: token}, true, nil
		default:
			return header, presented, nil
		}
	})
}

func UnsafeCookieWinsOverAuthorization(name string) auth.Option {
	return auth.LookupOrRefuse(func(get func(name string) string) (auth.Credential, bool, error) {
		if token, count := cookie(get("Cookie"), name); count == 1 {
			return auth.Credential{Scheme: auth.SchemeBearer, Token: token}, true, nil
		}
		credential, presented := auth.ParseAuthorization(get(auth.HeaderAuthorization))
		return credential, presented, nil
	})
}

func cookie(header, name string) (string, int) {
	if header == "" {
		return "", 0
	}
	parsed, err := http.ParseCookie(header)
	if err != nil {
		return "", 0
	}
	value, count := "", 0
	for _, candidate := range parsed {
		if candidate.Name != name {
			continue
		}
		value, count = candidate.Value, count+1
	}
	return value, count
}
