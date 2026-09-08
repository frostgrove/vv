# D-125 — A composed key is a wire format, and phase 1 freezes its rendering

**Status:** accepted
**Invariant:** `event.Compose` renders each part by escaping the separator `/`,
the escape byte `%`, and exactly what the kernel's text rule refuses — as
`%XX` with upper-case hex digits, byte by byte — and joins the escaped parts with
`/`. The rendering is part of every stream ever written under it and may not
change. The one pair of part lists that renders equal is no parts and one empty
part, and both are refused as empty at every door.

## The decision

```
Compose("acme", "A-17")       →  acme/A-17
Compose("acme/evil", "A-17")  →  acme%2Fevil/A-17
Compose("acme", "evil/A-17")  →  acme/evil%2FA-17
Compose("рога", "17")         →  рога/17
Compose("100%", "A-17")       →  100%25/A-17
Compose("a\x00b")             →  a%00b
Compose("a\x7fb")             →  a%7Fb
Compose("a\xffb")             →  a%FFb
Compose("a\u0085b")        →  a%C2%85b
Compose("acme")               →  acme
Compose("acme", "")           →  acme/
Compose("acme", "A-17", "2026") → acme/A-17/2026
```

An invalid UTF-8 byte is escaped as that byte; a multi-byte control rune is
escaped byte by byte, which is why U+0085 renders as two escapes. Nothing is
case-folded, trimmed or Unicode-normalised: keys compare as bytes.

**The escape set is the exact complement of the kernel's text rule and reads that
rule's own predicates**, so the two cannot drift apart. The result is therefore
legal text for any parts at all, which is what makes `Compose` the shortest
correct thing to write and a hand-rolled `a + "/" + b` the shorter incorrect one.

## Why it is frozen and what reopening it costs

A stream is `(family, key)` byte-exact. The key is the rendering, so the
rendering is **part of every stream ever written under it**. Change it and every
aggregate written under the old one is orphaned: the new key names a stream that
does not exist, the load returns the zero state at version 0, the append is
admitted, and there is no error at any point. A silent second history per
aggregate is the worst failure this subsystem has, and it is one edit away.

The only ways back are a store's key column and a migration over it — which is to
say, phase 2's problem and every deployment's downtime.

That is why it is decided here, on a whiteboard, before any stream exists. The
alternative was to wait for a real key column, and waiting costs more: after the
first stream is written the decision is not cheaper, it is impossible.

## The length-prefix alternative, and why it is not this

`cache/address.go:NamespaceOf` composes three parts by writing a big-endian
length before each and hashing the result. Length-prefixing is genuinely
injective with no escaping at all, and it looks like the better answer until the
difference is named:

**that prefix is hashed, never rendered.** A namespace is a digest; nobody reads
it, nothing sorts by it, and no operator ever types one into a `WHERE` clause. A
stream key is the opposite: it is stored as text, it appears in a store's index,
an operator greps for it, and a support agent reads it out over the phone. A
length-prefixed key is unreadable, unsearchable by prefix, and carries bytes no
column type is happy with.

Recording this here is the point of the section. It is the obvious improvement,
it has been proposed once, and it will be proposed again.

## Injectivity is the application's, and the framework makes it checkable

`Compose` guarantees that two distinct part **lists** never render one key. It
cannot guarantee that two distinct **identities** never produce one part list —
that is a property of the mapper the application wrote, and a framework cannot
see it.

Two identities rendering one key are two aggregates over one history, folding
each other's facts, with no error at any point. So the framework does the two
things it can: it makes the correct rendering the shortest one to write, and it
ships `eventtest.Keys`, which runs an application's own identities through its
own mapper and reports the pair that collides.

## What it forbids

- Do not change the rendering. Not the separator, not the escape byte, not the
  hex case, not the set of escaped bytes, not the treatment of an empty part.
- Do not add a length prefix, a hash, a version prefix or a normalisation step.
- Do not narrow the escape set to "what this store's column accepts". The set is
  derived from the kernel's text rule and a store's column is the store's
  problem.
- Do not add a `Decompose`. Reversibility is a property the fuzz target checks;
  it is not a surface, because a key that came back from a store is a store's
  data and parsing it would make an identity out of one.
- Do not hand-roll a key by concatenation in an application. That is what this
  exists to replace.

## Where it lives

- `event/identity.go` — `Compose`, `escapePart`, `escapeByte`, and the three
  constants the rendering is made of.
- `event/text.go` — `checkText`, `invalidRune`, `controlRune`: the rule the
  escape set is the complement of.

## Proven by

- `TestComposeRendersTheFrozenKey` — the twelve renderings above, byte for byte,
  and the one pair that renders equal asserted to be refused as empty.
- `FuzzComposeRendersAKeyThatIsLegalAndReversible` — for arbitrary parts the
  result passes the kernel's own text rule and the parts are recoverable, so
  neither property rests on the examples.
- `TestTwoFamiliesSharingOneKeyAreTwoStreams` — the other half of stream
  identity: the family is part of it.
- `FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey` — the proxy
  that reports what `Compose` cannot prevent.

## See also

[[D-121]] [[FL-036]] [[UC-032]]
