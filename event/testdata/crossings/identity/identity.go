package identity

import "github.com/frostgrove/vv/event"

type Alpha struct{ Count int64 }

type AlphaID struct{ Number string }

type BetaID struct{ Number string }

type Ticked struct{ By int64 }

var alphas = event.Define[Alpha]("crossings.alpha", func(id AlphaID) event.Key { return event.Compose(id.Number) })

var alphaTicked = event.Declare(alphas, "crossings.ticked", event.From(event.JSON[Ticked]()),
	func(this Alpha, tick Ticked) Alpha { this.Count += tick.By; return this })

func Advance() event.Change[Alpha] {
	return alphaTicked.New(BetaID{Number: "A-17"}, Ticked{By: 1})
}
