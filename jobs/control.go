package jobs

import "context"

type Controller interface {
	Cancel(context.Context, InvocationID) (DeliveryView, error)
	Terminate(context.Context, InvocationID) (DeliveryView, error)
}

type Operations interface {
	Admin
	Controller
}
