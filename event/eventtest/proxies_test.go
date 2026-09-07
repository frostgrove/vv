package eventtest_test

import (
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

type inventory struct {
	Count int
	Name  string
}

type counted struct{ By int }

type warehouseID struct {
	Region string
	Bin    string
}

func TestAnApplicationRunsTheThreeProxiesOverItsOwnDeclaration(t *testing.T) {
	composed := event.Define[inventory]("eventtest.warehouse", func(id warehouseID) event.Key {
		return event.Compose(id.Region, id.Bin)
	})
	counting := event.Declare(composed, "eventtest.counted", event.From(event.JSON[counted]()),
		func(state inventory, fact counted) inventory { state.Count += fact.By; return state })
	beside := event.Define[inventory]("eventtest.beside", func(id warehouseID) event.Key {
		return event.Compose(id.Region, id.Bin)
	})

	eventtest.RoundTrip(t, counting, counted{By: 7})
	eventtest.Keys(t, composed,
		warehouseID{Region: "eu/west", Bin: "17"},
		warehouseID{Region: "eu", Bin: "west/17"},
		warehouseID{Region: "eu", Bin: "18"})
	eventtest.Families(t, composed, beside)
}

// The control the three proxies need: each of them compares something, and a
// comparison that can never differ certifies nothing. A concatenating mapper is
// what Keys is for, so the collision it looks for is asserted to be there.
func TestTheProxiesCompareSomethingThatCanDiffer(t *testing.T) {
	concatenating := event.Define[inventory]("eventtest.concatenated", func(id warehouseID) event.Key {
		return event.Key(id.Region + "/" + id.Bin)
	})
	left, err := concatenating.Key(warehouseID{Region: "eu/west", Bin: "17"})
	if err != nil {
		t.Fatalf("rendering a key through a concatenating mapper answered %v", err)
	}
	right, err := concatenating.Key(warehouseID{Region: "eu", Bin: "west/17"})
	if err != nil {
		t.Fatalf("rendering a second key through a concatenating mapper answered %v", err)
	}
	if left != right {
		t.Fatal("two identities a concatenating mapper renders alike rendered two keys, so the collision eventtest.Keys exists to find cannot be built and the case above passes over nothing")
	}

	composed := event.Define[inventory]("eventtest.warehouse", func(id warehouseID) event.Key {
		return event.Compose(id.Region, id.Bin)
	})
	counting := event.Declare(composed, "eventtest.counted", event.From(event.JSON[counted]()),
		func(state inventory, fact counted) inventory { state.Count += fact.By; return state })

	escaped, err := composed.Key(warehouseID{Region: "eu/west", Bin: "17"})
	if err != nil {
		t.Fatalf("rendering the same identity through Compose answered %v", err)
	}
	if escaped == left {
		t.Fatal("Compose renders the key a concatenating mapper does, so the shortest correct rendering is the ambiguous one")
	}

	sample := counted{By: 7}
	if _, err := counting.RoundTrip(sample, sample); err == nil {
		t.Fatal("a fact of one revision accepted two samples, so RoundTrip counts nothing")
	}
}

// The control the section rule asks of every section and the three proxies were
// getting none of: each of them is driven through the branch it exists for, and
// a proxy whose detection is deleted reports nothing here rather than reporting
// a green run over an application that has the collision.
func TestEachProxyReportsTheThingItExistsToFind(t *testing.T) {
	concatenating := event.Define[inventory]("eventtest.concatenated", func(id warehouseID) event.Key {
		return event.Key(id.Region + "/" + id.Bin)
	})
	composed := event.Define[inventory]("eventtest.warehouse", func(id warehouseID) event.Key {
		return event.Compose(id.Region, id.Bin)
	})
	counting := event.Declare(composed, "eventtest.counted", event.From(event.JSON[counted]()),
		func(state inventory, fact counted) inventory { state.Count += fact.By; return state })
	twin := event.Define[inventory]("eventtest.warehouse", func(id warehouseID) event.Key {
		return event.Compose(id.Region, id.Bin)
	})
	elsewhere := event.Define[inventory]("eventtest.elsewhere", func(id warehouseID) event.Key {
		return event.Compose(id.Region, id.Bin)
	})
	unrendered := event.Define[inventory]("eventtest.unrendered", func(warehouseID) event.Key { return "" })

	for _, reported := range []struct {
		what string
		err  error
	}{
		{"a fact whose sample does not survive its own codec", eventtest.RoundTripAnswers(counting, counted{By: 7}, counted{By: 7})},
		{"no fact at all", eventtest.RoundTripAnswers[inventory, warehouseID, counted](nil)},
		{"two identities a concatenating mapper renders one key for",
			eventtest.KeysAnswers(concatenating, warehouseID{Region: "eu/west", Bin: "17"}, warehouseID{Region: "eu", Bin: "west/17"})},
		{"an identity that renders no legal key at all",
			eventtest.KeysAnswers(unrendered, warehouseID{Region: "eu", Bin: "17"}, warehouseID{Region: "eu", Bin: "18"})},
		{"one identity, which is not enough for injectivity to be a property of", eventtest.KeysAnswers(composed, warehouseID{Region: "eu", Bin: "17"})},
		{"no aggregate at all", eventtest.KeysAnswers[inventory, warehouseID](nil)},
		{"two declarations that name one family", eventtest.FamiliesAnswers(composed, twin)},
		{"a declaration that is nothing", eventtest.FamiliesAnswers(composed, nil)},
		{"no declarations at all", eventtest.FamiliesAnswers()},
	} {
		if reported.err == nil {
			t.Errorf("a proxy given %s reported nothing, so an application it certifies has the collision the proxy exists to find and is told it does not", reported.what)
		}
	}

	for _, silent := range []struct {
		what string
		err  error
	}{
		{"a fact whose sample survives its own codec", eventtest.RoundTripAnswers(counting, counted{By: 7})},
		{"three identities Compose renders three keys for", eventtest.KeysAnswers(composed,
			warehouseID{Region: "eu/west", Bin: "17"}, warehouseID{Region: "eu", Bin: "west/17"}, warehouseID{Region: "eu", Bin: "18"})},
		{"two declarations of two families", eventtest.FamiliesAnswers(composed, elsewhere)},
	} {
		if silent.err != nil {
			t.Errorf("a proxy given %s reported %v, so the reports above are not the collisions they name", silent.what, silent.err)
		}
	}
}
