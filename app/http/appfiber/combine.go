package appfiber

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

var ErrCombine = errors.New("routes cannot be contributed as one")

func Combine(routes ...Route) (Route, error) {
	parts := slices.Clone(routes)
	var problems []string
	if len(parts) == 0 {
		problems = append(problems, "nothing was given to combine")
	}

	owners := map[string]string{}
	for index, part := range parts {
		if part == nil {
			problems = append(problems, fmt.Sprintf("the part in position %d is nil", index))
			continue
		}
		for _, endpoint := range part.Access() {
			key := endpointKey(endpoint.Method, endpoint.Path)
			if owner, taken := owners[key]; taken {
				problems = append(problems,
					fmt.Sprintf("%s is declared by both %s and %T", key, owner, part))
				continue
			}
			owners[key] = fmt.Sprintf("%T", part)
		}
	}

	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, fmt.Errorf("%w:\n  - %s", ErrCombine, strings.Join(problems, "\n  - "))
	}
	return &combinedRoutes{parts: parts}, nil
}

func MustCombine(routes ...Route) Route {
	route, err := Combine(routes...)
	if err != nil {
		panic(err)
	}
	return route
}

type combinedRoutes struct {
	parts []Route
}

func (this *combinedRoutes) Mount(router fiber.Router) {
	for _, part := range this.parts {
		part.Mount(router)
	}
}

func (this *combinedRoutes) Access() []authhttp.Endpoint {
	var endpoints []authhttp.Endpoint
	for _, part := range this.parts {
		endpoints = append(endpoints, part.Access()...)
	}
	return endpoints
}

func (this *combinedRoutes) combined() []Route {
	return slices.Clone(this.parts)
}
