// Package eventtest is the conformance suite an event store runs against
// itself. A store implementer supplies a Factory and calls Run; the suite
// dispatches twenty named sections and reports each of them in one of three
// words — passed, not certified, failed — so a capability nobody claimed and a
// capability nobody could demonstrate are told apart from a pass.
//
// RunCheckpoints is the same contract over an event.Checkpoints, and it
// dispatches fourteen. Two of them are new in this release and a store that
// certified twelve before is asked both: topology, which is mandatory, and
// topology handoff, which runs for a store claiming Transactions. They are what
// a partitioned projection's split rests on — one cursor written under two
// names comes back unchanged under each, a save at advance 1 over a live row is
// refused by the store's own fence, and a read, two saves and a removal inside
// one caller-opened transaction are all or nothing.
//
// Keys, Families and RoundTrip are the three proxies an application runs over
// its own declaration: the identity mapper's injectivity, one family per
// aggregate, and a payload that survives its own codec.
package eventtest
