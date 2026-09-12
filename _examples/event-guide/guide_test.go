package eventguide

import (
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
)

func TestPayloadsRoundTrip(t *testing.T) {
	eventtest.RoundTrip(t, Placed, OrderPlaced{Total: 1200})
	eventtest.Keys(t, Orders, OrderID{"acme", "1"}, OrderID{"acme", "2"})
	eventtest.Families(t, Orders)
}
