package change

import "github.com/frostgrove/vv/event"

type Alpha struct{ Count int64 }

type Beta struct{ Count int64 }

type Ticked struct{ By int64 }

var alphas = event.Define[Alpha]("crossings.alpha", func(id string) event.Key { return event.Compose(id) })

var betas = event.Define[Beta]("crossings.beta", func(id string) event.Key { return event.Compose(id) })

var alphaTicked = event.Declare(alphas, "crossings.ticked", event.From(event.JSON[Ticked]()),
	func(this Alpha, tick Ticked) Alpha { this.Count += tick.By; return this })

func Advance() (Beta, error) {
	return betas.Fold("A-17", Beta{}, alphaTicked.New("A-17", Ticked{By: 1}))
}
