package i18n

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

func TestCatalogEnforcesLocalePluralCategories(t *testing.T) {
	valid := []struct {
		locale string
		mode   string
		key    string
	}{
		{locale: "en", mode: "plural", key: "one"},
		{locale: "ru", mode: "plural", key: "few"},
		{locale: "ar", mode: "plural", key: "two"},
		{locale: "pl", mode: "plural", key: "many"},
		{locale: "en", mode: "ordinal", key: "few"},
		{locale: "ru", mode: "ordinal", key: "other"},
		{locale: "ar", mode: "ordinal", key: "other"},
		{locale: "pl", mode: "ordinal", key: "other"},
		{locale: "en", mode: "plural", key: "9999999999999999999999999999999999999999"},
	}
	for _, test := range valid {
		t.Run(test.locale+"_"+test.mode+"_"+test.key, func(t *testing.T) {
			source := ".input {$n :number select=" + test.mode + "}\n.match $n\n" + test.key + " {{selected}}\n* {{other}}"
			_ = localeSnapshot(t, test.locale, source, ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true})
		})
	}

	invalid := []struct {
		locale string
		mode   string
		key    string
	}{
		{locale: "en", mode: "plural", key: "few"},
		{locale: "ru", mode: "plural", key: "zero"},
		{locale: "ar", mode: "plural", key: "bogus"},
		{locale: "pl", mode: "plural", key: "two"},
		{locale: "en", mode: "ordinal", key: "many"},
		{locale: "ru", mode: "ordinal", key: "one"},
		{locale: "ar", mode: "ordinal", key: "two"},
		{locale: "pl", mode: "ordinal", key: "few"},
	}
	for _, test := range invalid {
		t.Run("reject_"+test.locale+"_"+test.mode+"_"+test.key, func(t *testing.T) {
			source := ".input {$n :number select=" + test.mode + "}\n.match $n\n" + test.key + " {{selected}}\n* {{other}}"
			spec := CatalogSpec{
				Revision:        "catalog",
				SourceLocale:    test.locale,
				DefaultLocale:   test.locale,
				DefaultTimeZone: "UTC",
				Supported:       []string{test.locale},
				Modules:         []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("m", source, ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true})}}},
			}
			_, err := New(spec)
			if err == nil || !strings.Contains(err.Error(), "key is not valid for selector") {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestCatalogRejectsEveryExpressionAttribute(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		arguments []ArgumentSpec
	}{
		{name: "variable", source: "{$value @flag}", arguments: []ArgumentSpec{{Name: "value", Type: TypeText, Required: true}}},
		{name: "function", source: "{$value :string @flag=on}", arguments: []ArgumentSpec{{Name: "value", Type: TypeText, Required: true}}},
		{name: "literal", source: "{|fixed| @flag}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(testCatalog(simpleMessage("m", test.source, test.arguments...)))
			if err == nil || !strings.Contains(err.Error(), "expression attributes are outside the profile") {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestDescriptionIsRequiredAndSharesSourceDigestSemantics(t *testing.T) {
	for _, description := range []string{"", " \t\n"} {
		spec := testCatalog(simpleMessage("m", "source"))
		spec.Modules[0].Messages[0].Description = description
		if _, err := New(spec); err == nil || !strings.Contains(err.Error(), "translator description is required") {
			t.Fatalf("New() description %q error = %v", description, err)
		}
		message := spec.Modules[0].Messages[0]
		if _, err := ExpectedSourceDigestForLocale("", "en", "app", message); err == nil || !strings.Contains(err.Error(), "description") {
			t.Fatalf("ExpectedSourceDigestForLocale() description %q error = %v", description, err)
		}
	}

	message := simpleMessage("m", "source")
	message.Description = "Translator context \u202eis not rendered"
	first, err := ExpectedSourceDigestForLocale("", "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil || first != second {
		t.Fatalf("source digest = %q/%q, %v", first, second, err)
	}
	if _, err := New(testCatalog(message)); err != nil {
		t.Fatalf("non-rendered translator context was rejected: %v", err)
	}
}

func TestTranslationMayOmitRequiredDeclaredArgumentWhileBindStillRequiresIt(t *testing.T) {
	messageSpec := simpleMessage("welcome", "Hello {$name}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	messageSpec.Translations = []Translation{{Locale: "ru", Text: "Здравствуйте", Review: ReviewApproved, ContractRevision: messageSpec.Revision}}
	snapshot := mustSnapshot(t, testCatalog(messageSpec))
	if _, err := snapshot.Bind("app.welcome"); err == nil || !strings.Contains(err.Error(), "required argument") {
		t.Fatalf("Bind() without required argument error = %v", err)
	}
	message, err := snapshot.Bind("app.welcome", Text("name", "Ada"))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, mustView(t, snapshot, "ru", "", "", PresentationNoIsolation), message).Text; got != "Здравствуйте" {
		t.Fatalf("translation omitting required argument rendered %q", got)
	}
}

func TestTemplateStructuralLimitsAndLocalReferenceGraph(t *testing.T) {
	defaults := DefaultLimits()
	if defaults.MaxDeclarations < 1 || defaults.MaxVariants < 1 || defaults.MaxLocalDepth < 1 {
		t.Fatalf("zero limits did not receive structural defaults: %+v", defaults)
	}

	declarations := ".local $first = {|one|}\n.local $second = {$first}\n{{{$second}}}"
	limitedDeclarations := testCatalog(simpleMessage("m", declarations))
	limitedDeclarations.Limits.MaxDeclarations = 1
	if _, err := New(limitedDeclarations); err == nil || !strings.Contains(err.Error(), "limit") || !strings.Contains(err.Error(), "declarations") {
		t.Fatalf("declaration limit error = %v", err)
	}
	allowedDeclarations := limitedDeclarations
	allowedDeclarations.Limits.MaxDeclarations = 2
	if _, err := New(allowedDeclarations); err != nil {
		t.Fatalf("declaration boundary control failed: %v", err)
	}

	variants := ".input {$n :number select=exact}\n.match $n\n1 {{one}}\n2 {{two}}\n* {{other}}"
	limitedVariants := testCatalog(simpleMessage("m", variants, ArgumentSpec{Name: "n", Type: TypeInteger, Required: true}))
	limitedVariants.Limits.MaxVariants = 2
	if _, err := New(limitedVariants); err == nil || !strings.Contains(err.Error(), "limit") || !strings.Contains(err.Error(), "variants") {
		t.Fatalf("variant limit error = %v", err)
	}
	allowedVariants := limitedVariants
	allowedVariants.Limits.MaxVariants = 3
	if _, err := New(allowedVariants); err != nil {
		t.Fatalf("variant boundary control failed: %v", err)
	}

	limitedDepth := testCatalog(simpleMessage("m", declarations))
	limitedDepth.Limits.MaxLocalDepth = 1
	if _, err := New(limitedDepth); err == nil || !strings.Contains(err.Error(), "limit") || !strings.Contains(err.Error(), "local reference depth") {
		t.Fatalf("local depth error = %v", err)
	}
	allowedDepth := limitedDepth
	allowedDepth.Limits.MaxLocalDepth = 2
	if _, err := New(allowedDepth); err != nil {
		t.Fatalf("local depth boundary control failed: %v", err)
	}

	cycle := testCatalog(simpleMessage("m", ".local $first = {$second}\n.local $second = {$first}\n{{{$first}}}"))
	if _, err := New(cycle); err == nil || !strings.Contains(err.Error(), "local reference cycle") {
		t.Fatalf("local reference cycle error = %v", err)
	}

	for name, mutate := range map[string]func(*Limits){
		"declarations": func(limits *Limits) { limits.MaxDeclarations = -1 },
		"variants":     func(limits *Limits) { limits.MaxVariants = 65537 },
		"local depth":  func(limits *Limits) { limits.MaxLocalDepth = 257 },
	} {
		t.Run("invalid "+name, func(t *testing.T) {
			spec := testCatalog(simpleMessage("m", "ok"))
			mutate(&spec.Limits)
			if _, err := New(spec); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestStructuralLimitsAffectSnapshotIdentity(t *testing.T) {
	base := mustSnapshot(t, testCatalog(simpleMessage("m", "ok")))
	for name, mutate := range map[string]func(*Limits){
		"declarations": func(limits *Limits) { limits.MaxDeclarations = DefaultLimits().MaxDeclarations + 1 },
		"variants":     func(limits *Limits) { limits.MaxVariants = DefaultLimits().MaxVariants + 1 },
		"local depth":  func(limits *Limits) { limits.MaxLocalDepth = DefaultLimits().MaxLocalDepth + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			spec := testCatalog(simpleMessage("m", "ok"))
			mutate(&spec.Limits)
			if changed := mustSnapshot(t, spec); changed.Digest() == base.Digest() {
				t.Fatal("structural limit is absent from snapshot digest")
			}
		})
	}
}

func TestIntegerFunctionAcceptsOnlyExactIntegerSchemasAndOptions(t *testing.T) {
	decimal := testCatalog(simpleMessage("m", "{$n :integer}", ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true}))
	if _, err := New(decimal); err == nil || !strings.Contains(err.Error(), "does not accept decimal") {
		t.Fatalf(":integer decimal 1.5 schema error = %v", err)
	}

	for _, option := range []string{
		"minimumFractionDigits=0",
		"maximumFractionDigits=0",
		"minimumSignificantDigits=1",
		"roundingIncrement=1",
		"roundingMode=halfExpand",
		"roundingPriority=auto",
		"trailingZeroDisplay=auto",
		"notation=scientific",
		"compactDisplay=short",
	} {
		t.Run(option, func(t *testing.T) {
			spec := testCatalog(simpleMessage("m", "{$n :integer "+option+"}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true}))
			if _, err := New(spec); err == nil || !strings.Contains(err.Error(), "option integer.") {
				t.Fatalf("New() error = %v", err)
			}
		})
	}

	valid := testCatalog(simpleMessage("m", "{$n :integer minimumIntegerDigits=3 maximumSignificantDigits=2 useGrouping=false signDisplay=always}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true}))
	snapshot := mustSnapshot(t, valid)
	message, err := snapshot.Bind("app.m", Integer("n", 7))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message).Text; got != "+007" {
		t.Fatalf("valid integer formatting = %q", got)
	}
	for _, argumentType := range []ArgumentType{TypeInteger, TypeUnsignedInteger, TypeBigInteger} {
		if _, err := New(testCatalog(simpleMessage("m", "{$n :integer}", ArgumentSpec{Name: "n", Type: argumentType, Required: true}))); err != nil {
			t.Errorf(":integer rejected %s schema: %v", argumentType, err)
		}
	}
}

func TestNoIsolationCountsOnlyPublicParts(t *testing.T) {
	spec := testCatalog(simpleMessage("m", "{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Limits.MaxOutputParts = 1
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.m", Text("value", "value"))
	if err != nil {
		t.Fatal(err)
	}
	without := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	rendered, err := without.Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Text != "value" || len(rendered.Parts) != 1 || rendered.Parts[0].Kind != PartValue {
		t.Fatalf("no-isolation output = %+v", rendered)
	}
	with := mustView(t, snapshot, "en", "", "", PresentationDefault)
	if _, err := with.Render(context.Background(), message); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("default isolation one-part limit error = %v", err)
	}
}

type stagedCancelContext struct {
	calls    int
	cancelAt int
}

func (c *stagedCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *stagedCancelContext) Done() <-chan struct{}       { return nil }
func (c *stagedCancelContext) Value(any) any               { return nil }
func (c *stagedCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestAcceptLanguageBoundsRepresentationAndChecksCancellation(t *testing.T) {
	repeated := make([]string, 1_000_000)
	for i := range repeated {
		repeated[i] = "en"
	}
	choice := AcceptLanguage(SourceProtocol, repeated...)
	if !choice.limited || len(choice.values) != 0 {
		t.Fatalf("million-value choice retained input: limited=%v values=%d", choice.limited, len(choice.values))
	}

	huge := strings.Repeat("x", 1<<20)
	choice = AcceptLanguage(SourceProtocol, huge)
	if !choice.limited || len(choice.values) != 0 || choice.headerBytes > maxAcceptLanguageBytes {
		t.Fatalf("huge choice retained input: %+v", choice)
	}

	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en", Limits: LocaleLimits{MaxHeaderBytes: 4, MaxRanges: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.Resolve(AcceptLanguage(SourceProtocol, "en-US")); got.Outcome != OutcomeLimited || got.Reason != ReasonLimit {
		t.Fatalf("configured header limit = %+v", got)
	}
	if got := resolver.Resolve(choice); got.Outcome != OutcomeLimited || got.Reason != ReasonLimit {
		t.Fatalf("absolute header limit = %+v", got)
	}

	ctx := &stagedCancelContext{cancelAt: 4}
	got := resolver.ResolveContext(ctx, AcceptLanguage(SourceProtocol, "fr"))
	if got.Outcome != OutcomeCanceled || got.Reason != ReasonContextCanceled || got.Matched() {
		t.Fatalf("mid-parse cancellation = %+v after %d checks", got, ctx.calls)
	}
}

func TestPublicInputPreflightsRejectBeforeDeepValidation(t *testing.T) {
	parents := make([]LocaleEdge, 129)
	if _, err := NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en", Parents: parents}); err == nil || !strings.Contains(err.Error(), "parent edges exceed") {
		t.Fatalf("parent preflight error = %v", err)
	}

	base := simpleMessage("m", "source")
	tests := []struct {
		name    string
		message MessageSpec
	}{
		{name: "argument count", message: func() MessageSpec { m := base; m.Arguments = make([]ArgumentSpec, 257); return m }()},
		{name: "argument name bytes", message: func() MessageSpec {
			m := base
			m.Arguments = []ArgumentSpec{{Name: strings.Repeat("a", 1025), Type: TypeText}}
			return m
		}()},
		{name: "enum count", message: func() MessageSpec {
			m := base
			m.Arguments = []ArgumentSpec{{Name: "v", Type: TypeEnum, Values: make([]string, 4097)}}
			return m
		}()},
		{name: "markup count", message: func() MessageSpec { m := base; m.Markup = make([]string, 4097); return m }()},
		{name: "markup bytes", message: func() MessageSpec { m := base; m.Markup = []string{strings.Repeat("a", 1025)}; return m }()},
		{name: "source bytes", message: func() MessageSpec { m := base; m.Source = strings.Repeat("x", (4<<20)+1); return m }()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ExpectedSourceDigestForLocale("", "en", "app", test.message); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("ExpectedSourceDigestForLocale() error = %v", err)
			}
		})
	}

	spec := testCatalog(simpleMessage("m", "{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Limits.MaxArgumentBytes = 8
	snapshot := mustSnapshot(t, spec)
	if _, err := snapshot.Bind("app.m", Text("value", strings.Repeat("\xff", 16))); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized invalid UTF-8 argument error = %v", err)
	}

	for _, argument := range []Argument{
		Money("amount", strings.Repeat("1", maxDecimalBytes+1), "usd"),
		Money("amount", "1", strings.Repeat("u", 1<<20)),
	} {
		if argument.invalid == "" || argument.text != "" {
			t.Fatalf("invalid Money materialized text: invalid=%q bytes=%d", argument.invalid, len(argument.text))
		}
	}
}

func TestRenderPreflightStopsExpansionAndBudgetExhaustionIsSticky(t *testing.T) {
	observations := make([]Observation, 0, 1)
	spec := testCatalog(simpleMessage("m", "{$value}{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Limits.MaxOutputBytes = 4
	spec.Observer = func(_ context.Context, observation Observation) { observations = append(observations, observation) }
	snapshot := mustSnapshot(t, spec)
	record := snapshot.records["app.m"]
	translation := record.templates["en"]
	translation.formatter = nil
	record.templates["en"] = translation
	message, err := snapshot.Bind("app.m", Text("value", "abcd"))
	if err != nil {
		t.Fatal(err)
	}
	view := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	observations = observations[:0]
	_, err = view.Render(context.Background(), message)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("preflight error = %v", err)
	}
	if len(observations) != 1 || observations[0].Outcome != OutcomeLimited || observations[0].Reason != ReasonOutputLimit {
		t.Fatalf("preflight observation = %+v", observations)
	}

	budget := &renderBudget{exhausted: true}
	number := &exactNumberValue{
		value:         numericInput{kind: numericDecimal, decimal: "1"},
		grammarLocale: "invalid locale",
		formatLocale:  "invalid locale",
		style:         styleNumber,
		budget:        budget,
	}
	if _, err := number.ToString(); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("number resumed exhausted budget: %v", err)
	}
	date := &exactDateValue{value: time.Now(), formatLocale: "invalid locale", timeZone: "invalid", budget: budget}
	if _, err := date.ToString(); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("date resumed exhausted budget: %v", err)
	}
	text := &boundedStringValue{value: "x", budget: budget}
	if _, err := text.ToString(); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("text resumed exhausted budget: %v", err)
	}
}

func TestErrorMessagesObservesTerminalFailureClasses(t *testing.T) {
	assertObservation := func(t *testing.T, observations []Observation, outcome Outcome, reason Reason) {
		t.Helper()
		if len(observations) != 1 || observations[0].Operation != OperationRender || observations[0].Outcome != outcome || observations[0].Reason != reason || observations[0].Count != 1 {
			t.Fatalf("observations = %+v, want %s/%s", observations, outcome, reason)
		}
	}

	t.Run("invalid argument", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.limited", Params: []ErrorParam{{Param: "limit", Argument: "limit"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if text, ok := source.Message(context.Background(), errs.Violation{Code: "required", Params: map[string]any{"limit": "wrong"}}, "en"); ok || text != "" {
			t.Fatalf("invalid parameter rendered %q/%v", text, ok)
		}
		assertObservation(t, observations, OutcomeInvalid, ReasonInvalidArgument)
	})

	t.Run("missing template", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		source, err := snapshot.ErrorMessages(ErrorSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := source.Message(context.Background(), errs.Violation{Code: "absent"}, "en"); ok {
			t.Fatal("absent ladder rendered")
		}
		assertObservation(t, observations, OutcomeMissing, ReasonMissingTemplate)
	})

	t.Run("invalid violation", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		source, err := snapshot.ErrorMessages(ErrorSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := source.Message(context.Background(), errs.Violation{}, "en"); ok {
			t.Fatal("invalid violation rendered")
		}
		assertObservation(t, observations, OutcomeInvalid, ReasonInvalidArgument)
	})

	t.Run("output limit", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		spec := testCatalog(simpleMessage("error", "{$value}{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
		spec.Limits.MaxOutputBytes = 4
		spec.Observer = func(_ context.Context, observation Observation) { observations = append(observations, observation) }
		snapshot := mustSnapshot(t, spec)
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "app.error", Params: []ErrorParam{{Param: "value", Argument: "value"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := source.Message(context.Background(), errs.Violation{Code: "required", Params: map[string]any{"value": "abcd"}}, "en"); ok {
			t.Fatal("over-budget error rendered")
		}
		assertObservation(t, observations, OutcomeLimited, ReasonOutputLimit)
	})

	t.Run("context canceled", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.generic"}}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, ok := source.Message(ctx, errs.Violation{Code: "required"}, "en"); ok {
			t.Fatal("canceled message rendered")
		}
		assertObservation(t, observations, OutcomeCanceled, ReasonContextCanceled)
	})

	t.Run("template failure", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		record := snapshot.records["errors.generic"]
		translation := record.templates["en"]
		translation.formatter = nil
		record.templates["en"] = translation
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.generic"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := source.Message(context.Background(), errs.Violation{Code: "required"}, "en"); ok {
			t.Fatal("broken template rendered")
		}
		assertObservation(t, observations, OutcomeInvalid, ReasonTemplateFailure)
	})

	t.Run("successful fallback wins", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{
			{Ladder: "user.email.required", Key: "errors.limited", Params: []ErrorParam{{Param: "limit", Argument: "limit"}}},
			{Ladder: "required", Key: "errors.generic"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		violation := errs.Violation{Path: errs.Path{errs.Named("user"), errs.Named("email")}, Code: "required"}
		if text, ok := source.Message(context.Background(), violation, "en"); !ok || text != "generic" {
			t.Fatalf("fallback = %q/%v", text, ok)
		}
		assertObservation(t, observations, OutcomeSuccess, ReasonExact)
	})

	t.Run("locale resolution fallback remains observable", func(t *testing.T) {
		observations := make([]Observation, 0, 1)
		snapshot := errorCatalog(t, func(_ context.Context, observation Observation) { observations = append(observations, observation) })
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.email"}}})
		if err != nil {
			t.Fatal(err)
		}
		if text, actual, ok := source.MessageWithLocale(context.Background(), errs.Violation{Code: "required"}, "ru-RU"); !ok || text != "Электронная почта" || actual != "ru" {
			t.Fatalf("resolved fallback = %q/%q/%v", text, actual, ok)
		}
		assertObservation(t, observations, OutcomeFallback, ReasonLookup)
	})
}

func TestErrorMessagesValidatesFixedFormattingLocaleAtConstruction(t *testing.T) {
	snapshot := errorCatalog(t, nil)
	if _, err := snapshot.ErrorMessages(ErrorSpec{
		FormattingLocale: "ast",
		Mappings: []ErrorMapping{{
			Ladder: "required", Key: "errors.limited",
			Params: []ErrorParam{{Param: "limit", Argument: "limit"}},
		}},
	}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("numeric error formatting locale = %v", err)
	}
	if _, err := snapshot.ErrorMessages(ErrorSpec{
		FormattingLocale: "ast",
		Mappings:         []ErrorMapping{{Ladder: "required", Key: "errors.generic"}},
	}); err != nil {
		t.Fatalf("text-only error formatting locale = %v", err)
	}
}

func TestErrorTerminalKeepsStrongestFailure(t *testing.T) {
	terminal := newErrorTerminal()
	terminal.record(OutcomeInvalid, ReasonInvalidArgument)
	terminal.record(OutcomeMissing, ReasonMissingTemplate)
	terminal.record(OutcomeInvalid, ReasonTemplateFailure)
	terminal.record(OutcomeInvalid, ReasonInvalidArgument)
	if terminal.outcome != OutcomeInvalid || terminal.reason != ReasonTemplateFailure {
		t.Fatalf("terminal = %s/%s", terminal.outcome, terminal.reason)
	}
	terminal.record(OutcomeLimited, ReasonOutputLimit)
	terminal.record(OutcomeInvalid, ReasonTemplateFailure)
	if terminal.outcome != OutcomeLimited || terminal.reason != ReasonOutputLimit {
		t.Fatalf("limited terminal = %s/%s", terminal.outcome, terminal.reason)
	}
}
