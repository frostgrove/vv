// Package eventtest is the conformance suite an event store runs against
// itself. A store implementer supplies a Factory and calls Run; the suite
// dispatches twenty named sections and reports each of them in one of three
// words — passed, not certified, failed — so a capability nobody claimed and a
// capability nobody could demonstrate are told apart from a pass.
//
// Keys, Families and RoundTrip are the three proxies an application runs over
// its own declaration: the identity mapper's injectivity, one family per
// aggregate, and a payload that survives its own codec.
package eventtest
