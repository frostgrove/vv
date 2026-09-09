# UC-033 — Render one message correctly now or later

**Actor:** the application author presenting a message to a person
**Covered by:** [[FL-038]]

## Scenario

An application presents the same business fact in an HTTP error, a page, an
email and a delayed job. Its users choose different languages, its tenants may
change approved wording, and a deployment may publish a new translation while
an older request is still running. Counts need real grammar rather than a
singular/plural boolean; money, numbers and dates need the recipient's culture;
Arabic text containing a Latin identifier must remain readable.

The author wants to declare the message once as a stable, typed contract. A bad
translation must stop before traffic starts. Choosing a language, falling back,
formatting values and activating a release must be explainable without copying
private message data into telemetry. Rendering the same deferred intent later
must either reproduce the selected release deliberately or deliberately use the
current one; it must never depend on a process-global locale or whichever
request happened to populate a cache.

## What must hold

1. A module declares stable message identities, translator context, a versioned
   argument schema, output kind and translations as ordinary values. Importing
   it registers nothing and starts nothing.
2. The application assembles all module declarations and explicit overrides
   before serving or working. Duplicate identities, incompatible schemas,
   unsupported grammar, fallback cycles, invalid locale identifiers and missing
   required translations are one deterministic construction refusal, never a
   first-request surprise.
3. The compiled catalogue is immutable. An operation pins one complete
   catalogue revision and cannot observe a mixture of old and new translations,
   fallback policy or argument schemas, even while another revision is being
   prepared or activated.
4. Locale selection considers explicit choice, a verified user preference,
   protocol preferences, a tenant default and the application default in a
   declared order. It understands canonical language tags, weighted ranges,
   exclusions and wildcards, is bounded against hostile input, and returns the
   supported locale and source it actually selected.
5. Resolving a supported locale and finding a particular message are separate
   decisions. Message fallback walks a finite, validated parent graph and
   reports the locale and layer that actually supplied the template; requested
   text is never presented as proof of the language that was used.
6. A deferred message contains only a declared identity, its contract revision
   and typed arguments copied from the caller. It does not choose a locale in
   `String` or `Error`, does not silently become a durable wire format, and an
   arbitrary user-controlled string cannot become a catalogue lookup.
7. Missing, extra, null and wrongly typed arguments are distinguishable. A
   checked dynamic path accepts only a known identity and compatible schema;
   generated helpers give ordinary application code a typed argument struct.
   A hand-written typed struct may be compiled once during assembly without
   runtime registration or field discovery on every bind; its field mapping,
   exact value shapes and optional/null topology are rejected eagerly.
8. Cardinal and ordinal plural categories, exact numeric matches, string select
   and nested or multiple selectors follow the declared grammar profile. A
   required fallback branch is checked at construction, not guessed at render
   time.
9. Integer and decimal selection is exact. Large signed and unsigned integers
   are not rounded through a floating-point value, and lexical decimals retain
   the difference between values such as `1` and `1.0` where plural rules use
   visible fraction digits.
10. Message language and formatting culture are independent. Number, percent,
    currency, date and time formatting uses the view's explicit formatting
    policy, while grammatical selection uses the actual template language.
    Money is never converted to another currency, a calendar date never shifts
    through a time zone, and an instant never reads the machine's local zone.
    Standalone and named formatting policies preserve the same boundary.
    Numeric and date/time ranges retain structural endpoint provenance, while
    plural-range selection follows the locale's range rules.
11. Plain text is returned as plain text and is escaped by its eventual sink.
    Rich output is a bounded sequence of structural parts whose elements were
    allowlisted by the message contract; no translation becomes trusted HTML.
    Parts preserve MF2 type, direction and optional opaque authored identity;
    exact number/date subparts are copied and count against the same node bound.
12. Interpolated text is directionally isolated according to an explicit
    presentation policy. Right-to-left text and left-to-right identifiers remain
    readable without deleting legitimate Unicode directionality from authored
    content.
13. Every input and produced artefact is bounded: catalogue files and bytes,
    messages, locales, arguments, template depth, fallback depth, output bytes,
    rich parts and diagnostic steps. Invalid UTF-8 and duplicate object members
    are refused rather than normalized silently.
14. Application and tenant wording overrides replace a whole declared message
    in an allowed locale. They cannot change its identity, schema, output kind,
    machine error code, visibility or permission to be overridden, and applying
    one produces another immutable catalogue without carrying tenant identity.
15. Localizing a structured failure changes human wording only. Its status,
    kind, code, public path, partial marker and internal-error redaction remain
    unchanged across every full CRUD operation and every supported HTTP or gRPC
    binding. A formatting failure falls through to the existing safe message
    rather than replacing the business failure.
    When a view was already resolved for the operation, its error source uses
    that exact view and cannot negotiate a second locale from a transport hint.
16. One process-level error policy can reach generated resources, hand-written
    endpoints, authentication and access refusals, router refusals and unary or
    streaming RPC failures. A resource-specific wording option composes with the
    public path mapping generated for that resource; replacing the response
    shape cannot silently restore model field names.
17. A successful render reports its actual template locale and catalogue
    revision. Cache identity includes every input that can alter output,
    including message and formatting locales, zone, presentation, arguments and
    revision; cache identity grants no authority and shared work inherits no
    request identity.
18. A bounded explanation can show preference outcomes, fallback steps, winning
    layer, message identity, grammar profile and revision without exposing raw
    protocol headers, rendered text, arguments, validation paths, caller
    identity or tenant and user data.
19. Observation is optional, synchronous and panic-isolated. It emits one
    terminal result for a public operation using closed, bounded dimensions and
    never includes message identities, catalogue hashes, locale input, text,
    arguments, paths, causes or principals.
20. Publication validates a complete candidate before one atomic compare-and-
    swap. A failed or stale publication leaves the last known good revision
    active; rollback swaps a whole retained revision; views already in flight
    remain valid. Constructors create no watcher or background goroutine.
21. Authoring checks, pseudolocales and generated Go or frontend contracts are
    deterministic and runnable without a database, network or executing the
    application. Public export contains only identities explicitly declared
    public and cannot widen the runtime grammar silently. Its generational
    publisher and reader walk hierarchy entries from the filesystem or volume
    root through stable descriptors, refuse links and inspection/open identity
    changes, and cannot be redirected by a later ancestor replacement. A
    missing final publication root is created and its parent synced through the
    pinned parent descriptor. The reader enumerates one bounded fixed file set
    and stops bounded reads after cancellation.
    Usage extraction produces separate call-site evidence for checking; it is
    not a catalogue input to review. Source merge preserves structurally
    eligible authoring work, including stale/rejected identities for later
    classification, and requires explicit intent before discarding obsolete
    entries.
22. A delayed delivery records an application-owned intent and an explicit
    current-or-pinned presentation policy. Processing restores and authorizes
    its recipient before selecting a catalogue and locale; a catalogue pin is
    never treated as authorization.
23. An application that does not use internationalization neither compiles nor
    downloads its engine. The base error and transport contracts remain usable
    with the standard library alone.

## Out of scope

- Translating user documents, CMS fields or database rows; localized search,
  collation, URLs and SEO each need their own storage and publication policy.
- Currency conversion, address or person-name formatting, transliteration and
  machine translation.
- Choosing a tenant, authenticating a principal or authorizing a recipient.
- Owning object storage, a translation service, a file watcher, a job backend,
  a cache backend, telemetry SDKs or process health policy. The application
  composes those existing seams explicitly.
- Treating a Go deferred message as a permanent event or cross-language wire
  contract. Durable payloads remain application-owned and versioned.
