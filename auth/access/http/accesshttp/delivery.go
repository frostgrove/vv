package accesshttp

import (
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

const DeliveryHeader = "X-Auth-Delivery"

type Delivery string

const (
	DeliverCookies Delivery = "cookies"

	DeliverRefreshCookie Delivery = "refresh-cookie"

	DeliverBody Delivery = "body"
)

func (this Delivery) AccessInCookie() bool { return this == DeliverCookies }

func (this Delivery) RefreshInCookie() bool {
	return this == DeliverCookies || this == DeliverRefreshCookie
}

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

func (this Credentials) Default() Delivery {
	if this.cookies {
		return DeliverCookies
	}
	return DeliverBody
}

func Rotating(requested Delivery, byCookie bool) Delivery {
	if !byCookie {
		return DeliverBody
	}
	if requested.AccessInCookie() {
		return DeliverCookies
	}
	return DeliverRefreshCookie
}
