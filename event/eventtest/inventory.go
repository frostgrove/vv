package eventtest

import "github.com/frostgrove/vv/event"

// A section is a t.Run subtest and go test -list cannot see one, so a suite
// shipped with twelve of the twenty written prints twelve passing lines and
// looks complete. The inventory is what closes that: one slice, built here so
// nothing is package-level and mutable, iterated by the runner, and asserted
// against by a test that reads the report rather than the list.
type section struct {
	name  string
	needs func(event.Capabilities, Factory) string
	run   func(*probe)
}

func inventory() []section {
	return []section{
		{name: "binding", run: bindingSection},
		{name: "stream identity", run: streamIdentitySection},
		{name: "expected version", run: expectedVersionSection},
		{name: "dense versions", run: denseVersionsSection},
		{name: "global order", run: globalOrderSection},
		{name: "conservation", run: conservationSection},
		{name: "stream paging", run: streamPagingSection},
		{name: "global paging", run: globalPagingSection},
		{name: "resumption", run: resumptionSection},
		{name: "bounds", run: boundsSection},
		{name: "payload ownership", run: payloadOwnershipSection},
		{name: "refusal classes", run: refusalClassesSection},
		{name: "cancellation", run: cancellationSection},
		{name: "lifecycle", run: lifecycleSection},
		{name: "concurrency", run: concurrencySection},
		{name: "transactions", needs: needsTransactions, run: transactionsSection},
		{name: "durability", needs: needsPersistence, run: durabilitySection},
		{name: "shared backing", needs: needsSharedBacking, run: sharedBackingSection},
		{name: "monotone visibility", needs: needsMonotoneVisibility, run: monotoneVisibilitySection},
		{name: "store failure classification", needs: needsFailHook, run: storeFailureSection},
	}
}

func needsTransactions(capabilities event.Capabilities, _ Factory) string {
	if capabilities.Transactions != event.Supported {
		return "this store does not claim transactions"
	}
	return ""
}

func needsPersistence(capabilities event.Capabilities, _ Factory) string {
	if capabilities.Persistence != event.Supported {
		return "this store does not claim persistence, so nothing of it survives a restart"
	}
	return ""
}

func needsSharedBacking(capabilities event.Capabilities, _ Factory) string {
	if capabilities.SharedBacking != event.Supported {
		return "this store does not claim a shared backing, so it has no second value to be one store with"
	}
	return ""
}

func needsMonotoneVisibility(capabilities event.Capabilities, _ Factory) string {
	if capabilities.MonotoneVisibility != event.Supported {
		return "this store does not promise monotone visibility"
	}
	return ""
}

func needsFailHook(_ event.Capabilities, factory Factory) string {
	if factory.Fail == nil {
		return "this factory supplies no Fail hook, so no failure of this store's own can be driven"
	}
	return ""
}
