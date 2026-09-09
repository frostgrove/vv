package i18n

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
	"golang.org/x/text/unicode/norm"
)

type Presentation uint8

const (
	PresentationDefault Presentation = iota
	PresentationNoIsolation
)

func (p Presentation) String() string {
	switch p {
	case PresentationDefault:
		return "default"
	case PresentationNoIsolation:
		return "no_isolation"
	default:
		return "unknown"
	}
}

func (p Presentation) Valid() bool { return p == PresentationDefault || p == PresentationNoIsolation }

type ViewSpec struct {
	Resolution       Resolution
	FormattingLocale string
	TimeZone         string
	Presentation     Presentation
}

type View struct {
	snapshot         *Snapshot
	resolution       Resolution
	formattingLocale string
	timeZone         string
	presentation     Presentation
}

type PartKind uint8

const (
	PartText PartKind = iota + 1
	PartValue
	PartMarkupOpen
	PartMarkupClose
	PartMarkupStandalone
	PartBidiIsolation
)

func (k PartKind) String() string {
	switch k {
	case PartText:
		return "text"
	case PartValue:
		return "value"
	case PartMarkupOpen:
		return "markup_open"
	case PartMarkupClose:
		return "markup_close"
	case PartMarkupStandalone:
		return "markup_standalone"
	case PartBidiIsolation:
		return "bidi_isolation"
	default:
		return "unknown"
	}
}

func (k PartKind) Valid() bool { return k >= PartText && k <= PartBidiIsolation }

type Subpart struct {
	Type string
	Text string
}

const maximumPartTypeBytes = 128

type Part struct {
	Kind      PartKind
	Type      string
	Text      string
	Name      string
	ID        string
	Locale    string
	Direction string
	Subparts  []Subpart
}

type Rendered struct {
	Text             string
	Parts            []Part
	TemplateLocale   string
	ResolvedLocale   string
	ResolutionSource ChoiceSource
	ResolutionReason Reason
	Revision         string
	Digest           string
	Layer            Layer
	Outcome          Outcome
	RenderKey        string
}

type Explanation struct {
	Key               Key
	Source            ChoiceSource
	ResolvedLocale    string
	TemplateLocale    string
	Layer             Layer
	ResolutionOutcome Outcome
	ResolutionReason  Reason
	TemplateOutcome   Outcome
	TemplateReason    Reason
	Profile           string
	Revision          string
	PreferenceSteps   []PreferenceStep
	Steps             []ExplainStep
	Truncated         bool
}

type ExplainStep struct {
	Locale  string
	Present bool
}

func (s *Snapshot) For(locale string) (*View, error) {
	return s.ForContext(context.Background(), Exact(SourceExplicit, locale))
}

func (s *Snapshot) ForContext(ctx context.Context, choices ...Choice) (*View, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: snapshot is nil", ErrInvalidCatalog)
	}
	resolution := s.ResolveContext(ctx, choices...)
	if !resolution.Matched() {
		return nil, fmt.Errorf("%w: locale resolution ended with %s/%s", ErrInvalidLocale, resolution.Outcome.String(), resolution.Reason.String())
	}
	return s.View(ViewSpec{Resolution: resolution})
}

func (s *Snapshot) View(spec ViewSpec) (*View, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: snapshot is nil", ErrInvalidCatalog)
	}
	if !spec.Resolution.Matched() {
		return nil, fmt.Errorf("%w: resolution did not select a locale", ErrInvalidLocale)
	}
	if !validResolution(spec.Resolution) {
		return nil, fmt.Errorf("%w: resolution metadata is inconsistent", ErrInvalidLocale)
	}
	if !s.resolver.owns(spec.Resolution) {
		return nil, fmt.Errorf("%w: resolution belongs to another resolver", ErrInvalidLocale)
	}
	_, resolved, err := canonicalLocale(spec.Resolution.Locale, s.limits.Locale.MaxTagBytes)
	if err != nil || !slices.Contains(s.supported, resolved) {
		return nil, fmt.Errorf("%w: resolved locale is not supported", ErrInvalidLocale)
	}
	formattingLocale := spec.FormattingLocale
	if formattingLocale == "" {
		formattingLocale = resolved
	}
	_, formattingLocale, err = canonicalLocale(formattingLocale, s.limits.Locale.MaxTagBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: formatting locale: %v", ErrInvalidLocale, err)
	}
	if formattingLocale != resolved {
		if err := validateFormatRequirements(formattingLocale, s.formatRequirements[resolved]); err != nil {
			return nil, fmt.Errorf("%w: formatting locale: %v", ErrInvalidLocale, err)
		}
	}
	timeZone := spec.TimeZone
	if timeZone == "" {
		timeZone = s.defaultTimeZone
	}
	timeZone, err = canonicalTimeZone(timeZone)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if timeZone != "UTC" && s.timeZoneDataVersion == "" {
		return nil, fmt.Errorf("%w: non-UTC formatting requires a time-zone data version", ErrInvalidMessage)
	}
	if spec.Presentation != PresentationDefault && spec.Presentation != PresentationNoIsolation {
		return nil, fmt.Errorf("%w: presentation is invalid", ErrInvalidMessage)
	}
	return &View{
		snapshot:         s,
		resolution:       spec.Resolution,
		formattingLocale: formattingLocale,
		timeZone:         timeZone,
		presentation:     spec.Presentation,
	}, nil
}

func validResolution(resolution Resolution) bool {
	if !validChoiceSource(resolution.Source) {
		return false
	}
	switch resolution.Outcome {
	case OutcomeSuccess:
		return resolution.Reason == ReasonExact || resolution.Reason == ReasonWildcard
	case OutcomeFallback:
		return resolution.Reason == ReasonLookup || resolution.Reason == ReasonBestFit
	case OutcomeDefault:
		return resolution.Source == SourceApplication && resolution.Reason == ReasonPolicyDefault
	default:
		return false
	}
}

func (v *View) Render(ctx context.Context, message Message) (rendered Rendered, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	outcome := OutcomeInvalid
	reason := ReasonTemplateFailure
	defer func() {
		if recovered := recover(); recovered != nil {
			rendered = Rendered{}
			err = fmt.Errorf("%w: renderer panic", ErrInvalidMessage)
			outcome = OutcomeInvalid
			reason = ReasonTemplateFailure
		}
		if v != nil && v.snapshot != nil {
			notifyObserver(ctx, v.snapshot.observer, Observation{Operation: OperationRender, Outcome: outcome, Reason: reason, Duration: time.Since(started), Count: 1})
		}
	}()

	rendered, outcome, reason, err = v.render(ctx, message)
	return rendered, err
}

func (v *View) render(ctx context.Context, message Message) (Rendered, Outcome, Reason, error) {
	if v == nil || v.snapshot == nil {
		return Rendered{}, OutcomeInvalid, ReasonTemplateFailure, fmt.Errorf("%w: view is nil", ErrInvalidMessage)
	}
	if err := ctx.Err(); err != nil {
		outcome, reason := contextOperationResult(err)
		return Rendered{}, outcome, reason, err
	}
	record, ok := v.snapshot.records[message.key]
	if !ok {
		return Rendered{}, OutcomeMissing, ReasonMissingTemplate, fmt.Errorf("%w: %q", ErrNotFound, message.key)
	}
	if message.revision != record.descriptor.Revision || message.digest != record.contractHash {
		return Rendered{}, OutcomeInvalid, ReasonSchemaMismatch, fmt.Errorf("%w: message contract no longer matches", ErrInvalidMessage)
	}
	arguments, err := checkArguments(record.descriptor.Arguments, message.arguments, v.snapshot.limits)
	if err != nil {
		return Rendered{}, OutcomeInvalid, ReasonInvalidArgument, err
	}
	translation, _, ok := v.lookup(record)
	if !ok {
		return Rendered{}, OutcomeMissing, ReasonMissingTemplate, fmt.Errorf("%w: %q has no translation", ErrNotFound, message.key)
	}
	return v.renderTranslation(ctx, message, record, translation, arguments)
}

func (v *View) renderTranslation(ctx context.Context, message Message, record *messageRecord, translation compiledTranslation, arguments []Argument) (Rendered, Outcome, Reason, error) {
	budget := &renderBudget{bytes: v.snapshot.limits.MaxOutputBytes, parts: v.snapshot.limits.MaxOutputParts}
	if err := translation.work.check(ctx, record.descriptor.Arguments, arguments, v.snapshot.limits, v.presentation, budget); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			outcome, reason := contextOperationResult(contextErr)
			return Rendered{}, outcome, reason, contextErr
		}
		if errors.Is(err, ErrLimitExceeded) {
			return Rendered{}, OutcomeLimited, ReasonOutputLimit, fmt.Errorf("%w: output work exceeds configured bounds", ErrLimitExceeded)
		}
		return Rendered{}, OutcomeInvalid, ReasonInvalidArgument, err
	}
	values, err := v.renderValues(ctx, translation.locale, record.descriptor.Arguments, arguments, budget)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			outcome, reason := contextOperationResult(contextErr)
			return Rendered{}, outcome, reason, contextErr
		}
		if errors.Is(err, ErrLimitExceeded) {
			return Rendered{}, OutcomeLimited, ReasonOutputLimit, err
		}
		return Rendered{}, OutcomeInvalid, ReasonInvalidArgument, err
	}
	parts, err := translation.formatter.FormatToParts(values)
	if err != nil {
		if errors.Is(err, ErrLimitExceeded) {
			return Rendered{}, OutcomeLimited, ReasonOutputLimit, fmt.Errorf("%w: %v", ErrLimitExceeded, err)
		}
		return Rendered{}, OutcomeInvalid, ReasonTemplateFailure, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	converted, text, err := v.convertParts(ctx, record.descriptor, parts)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			outcome, reason := contextOperationResult(contextErr)
			return Rendered{}, outcome, reason, contextErr
		}
		if errors.Is(err, ErrLimitExceeded) {
			return Rendered{}, OutcomeLimited, ReasonOutputLimit, err
		}
		return Rendered{}, OutcomeInvalid, ReasonTemplateFailure, err
	}
	if err := ctx.Err(); err != nil {
		outcome, reason := contextOperationResult(err)
		return Rendered{}, outcome, reason, err
	}
	outcome := OutcomeSuccess
	reason := ReasonExact
	if translation.locale != v.resolution.Locale {
		outcome = OutcomeFallback
		reason = ReasonLookup
	}
	renderKey, err := v.renderKey(message)
	if err != nil {
		return Rendered{}, OutcomeInvalid, ReasonInvalidArgument, err
	}
	return Rendered{
		Text:             text,
		Parts:            converted,
		TemplateLocale:   translation.locale,
		ResolvedLocale:   v.resolution.Locale,
		ResolutionSource: v.resolution.Source,
		ResolutionReason: v.resolution.Reason,
		Revision:         v.snapshot.revision,
		Digest:           v.snapshot.digest,
		Layer:            translation.layer,
		Outcome:          outcome,
		RenderKey:        renderKey,
	}, outcome, reason, nil
}

func (v *View) RenderKey(message Message) (string, error) {
	if v == nil || v.snapshot == nil {
		return "", fmt.Errorf("%w: view is nil", ErrInvalidMessage)
	}
	record, ok := v.snapshot.records[message.key]
	if !ok || message.revision != record.descriptor.Revision || message.digest != record.contractHash {
		return "", fmt.Errorf("%w: message contract does not match", ErrInvalidMessage)
	}
	if _, err := checkArguments(record.descriptor.Arguments, message.arguments, v.snapshot.limits); err != nil {
		return "", err
	}
	return v.renderKey(message)
}

func (v *View) renderKey(message Message) (string, error) {
	hash := sha256.New()
	writeDigest(hash.Write, v.snapshot.digest)
	writeDigest(hash.Write, v.snapshot.revision)
	writeDigest(hash.Write, string(message.key))
	writeDigest(hash.Write, message.revision)
	writeDigest(hash.Write, v.resolution.Locale)
	writeDigest(hash.Write, strconv.Itoa(int(v.resolution.Source)))
	writeDigest(hash.Write, strconv.Itoa(int(v.resolution.Outcome)))
	writeDigest(hash.Write, strconv.Itoa(int(v.resolution.Reason)))
	writeDigest(hash.Write, v.formattingLocale)
	writeDigest(hash.Write, v.timeZone)
	writeDigest(hash.Write, v.snapshot.timeZoneDataVersion)
	writeDigest(hash.Write, strconv.Itoa(int(v.presentation)))
	for _, argument := range message.arguments {
		writeDigest(hash.Write, argument.name)
		writeDigest(hash.Write, argument.typeOf.String())
		writeDigest(hash.Write, strconv.FormatBool(argument.null))
		if argument.null {
			continue
		}
		switch argument.typeOf {
		case TypeText, TypeDecimal, TypeMoney, TypeEnum:
			writeDigest(hash.Write, argument.text)
		case TypeBool:
			writeDigest(hash.Write, strconv.FormatBool(argument.boolean))
		case TypeInteger:
			writeDigest(hash.Write, strconv.FormatInt(argument.signed, 10))
		case TypeUnsignedInteger:
			writeDigest(hash.Write, strconv.FormatUint(argument.unsigned, 10))
		case TypeBigInteger:
			if argument.big == nil {
				return "", fmt.Errorf("%w: nil big integer", ErrInvalidMessage)
			}
			writeDigest(hash.Write, argument.big.String())
		case TypeDate:
			writeDigest(hash.Write, fmt.Sprintf("%04d-%02d-%02d", argument.date.Year, argument.date.Month, argument.date.Day))
		case TypeInstant:
			writeDigest(hash.Write, argument.instant.UTC().Format(time.RFC3339Nano))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (v *View) Explain(message Message) (Explanation, error) {
	if v == nil || v.snapshot == nil {
		return Explanation{}, fmt.Errorf("%w: view is nil", ErrInvalidMessage)
	}
	record, ok := v.snapshot.records[message.key]
	if !ok {
		return Explanation{}, fmt.Errorf("%w: %q", ErrNotFound, message.key)
	}
	if message.revision != record.descriptor.Revision || message.digest != record.contractHash {
		return Explanation{}, fmt.Errorf("%w: message contract does not match", ErrInvalidMessage)
	}
	translation, steps, found := v.lookup(record)
	preferenceSteps := v.resolution.preferenceSteps
	maximum := v.snapshot.limits.MaxExplainSteps
	truncated := len(preferenceSteps)+len(steps) > maximum
	if len(preferenceSteps) > maximum {
		preferenceSteps = preferenceSteps[:maximum]
		steps = nil
	} else if len(steps) > maximum-len(preferenceSteps) {
		steps = steps[:maximum-len(preferenceSteps)]
	}
	explanation := Explanation{
		Key:               message.key,
		Source:            v.resolution.Source,
		ResolvedLocale:    v.resolution.Locale,
		ResolutionOutcome: v.resolution.Outcome,
		ResolutionReason:  v.resolution.Reason,
		TemplateOutcome:   OutcomeMissing,
		TemplateReason:    ReasonMissingTemplate,
		Profile:           v.snapshot.profile,
		Revision:          v.snapshot.revision,
		PreferenceSteps:   slices.Clone(preferenceSteps),
		Steps:             slices.Clone(steps),
		Truncated:         truncated,
	}
	if found {
		explanation.TemplateLocale = translation.locale
		explanation.Layer = translation.layer
		explanation.TemplateOutcome = OutcomeSuccess
		explanation.TemplateReason = ReasonExact
		if translation.locale != v.resolution.Locale {
			explanation.TemplateOutcome = OutcomeFallback
			explanation.TemplateReason = ReasonLookup
		}
	}
	return explanation, nil
}

func (v *View) lookup(record *messageRecord) (compiledTranslation, []ExplainStep, bool) {
	chain := v.snapshot.fallbackChain(v.resolution.Locale)
	steps := make([]ExplainStep, 0, len(chain))
	for _, locale := range chain {
		translation, ok := record.templates[locale]
		steps = append(steps, ExplainStep{Locale: locale, Present: ok})
		if ok {
			return translation, steps, true
		}
	}
	return compiledTranslation{}, steps, false
}

func (s *Snapshot) fallbackChain(start string) []string {
	chain := make([]string, 0, s.limits.Locale.MaxFallbackDepth+2)
	seen := make(map[string]bool)
	add := func(locale string) bool {
		if locale == "" || seen[locale] {
			return false
		}
		seen[locale] = true
		chain = append(chain, locale)
		return true
	}
	current := start
	add(current)
	for depth := 0; depth < s.limits.Locale.MaxFallbackDepth; depth++ {
		parent, ok := effectiveLocaleParent(current, s.parents)
		if !ok {
			break
		}
		if !add(parent) {
			break
		}
		current = parent
	}
	add(s.defaultLocale)
	add(s.sourceLocale)
	return chain
}

func (v *View) renderValues(ctx context.Context, grammarLocale string, specs []ArgumentSpec, arguments []Argument, budget *renderBudget) (map[string]any, error) {
	byName := make(map[string]Argument, len(arguments))
	for _, argument := range arguments {
		byName[argument.name] = argument
	}
	values := make(map[string]any, len(specs))
	for index, spec := range specs {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		argument, present := byName[spec.Name]
		if !present {
			values[spec.Name] = &nullMessageValue{source: "$" + spec.Name, locale: v.formattingLocale, budget: budget}
			continue
		}
		source := "$" + argument.name
		if argument.null {
			values[argument.name] = &nullMessageValue{source: source, locale: v.formattingLocale, budget: budget}
			continue
		}
		switch argument.typeOf {
		case TypeText, TypeEnum:
			values[argument.name] = &boundedStringValue{value: argument.text, locale: v.formattingLocale, source: source, budget: budget}
		case TypeBool:
			values[argument.name] = &boundedStringValue{value: strconv.FormatBool(argument.boolean), locale: v.formattingLocale, source: source, budget: budget}
		case TypeInteger:
			values[argument.name] = &exactNumberValue{value: numericInput{kind: numericSigned, signed: argument.signed}, grammarLocale: grammarLocale, formatLocale: v.formattingLocale, source: source, style: styleNumber, budget: budget}
		case TypeUnsignedInteger:
			values[argument.name] = &exactNumberValue{value: numericInput{kind: numericUnsigned, unsigned: argument.unsigned}, grammarLocale: grammarLocale, formatLocale: v.formattingLocale, source: source, style: styleNumber, budget: budget}
		case TypeBigInteger:
			values[argument.name] = &exactNumberValue{value: numericInput{kind: numericBig, big: newBigInt(argument.big)}, grammarLocale: grammarLocale, formatLocale: v.formattingLocale, source: source, style: styleNumber, budget: budget}
		case TypeDecimal:
			values[argument.name] = &exactNumberValue{value: numericInput{kind: numericDecimal, decimal: argument.text}, grammarLocale: grammarLocale, formatLocale: v.formattingLocale, source: source, style: styleNumber, budget: budget}
		case TypeMoney:
			amount, currency, ok := strings.Cut(argument.text, "\x00")
			if !ok {
				return nil, fmt.Errorf("%w: malformed Money argument", ErrInvalidMessage)
			}
			values[argument.name] = &exactNumberValue{value: numericInput{kind: numericDecimal, decimal: amount}, grammarLocale: grammarLocale, formatLocale: v.formattingLocale, source: source, style: styleCurrency, currency: currency, budget: budget}
		case TypeDate:
			if !v.snapshot.capabilities[CapabilityDateTime] {
				return nil, fmt.Errorf("%w: date/time capability is disabled", ErrInvalidMessage)
			}
			value := time.Date(argument.date.Year, argument.date.Month, argument.date.Day, 12, 0, 0, 0, time.UTC)
			values[argument.name] = &exactDateValue{value: value, dateOnly: true, formatLocale: v.formattingLocale, timeZone: "UTC", source: source, style: styleDate, budget: budget}
		case TypeInstant:
			if !v.snapshot.capabilities[CapabilityDateTime] {
				return nil, fmt.Errorf("%w: date/time capability is disabled", ErrInvalidMessage)
			}
			values[argument.name] = &exactDateValue{value: argument.instant.UTC(), formatLocale: v.formattingLocale, timeZone: v.timeZone, source: source, style: styleDateTime, budget: budget}
		default:
			return nil, fmt.Errorf("%w: unsupported argument type", ErrInvalidMessage)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func (v *View) convertParts(ctx context.Context, descriptor Descriptor, source []messagevalue.MessagePart) ([]Part, string, error) {
	parts := make([]Part, 0, min(len(source), v.snapshot.limits.MaxOutputParts))
	var text strings.Builder
	nodes := 0
	for index, sourcePart := range source {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
		}
		if sourcePart == nil {
			return nil, "", fmt.Errorf("%w: formatter returned a nil part", ErrInvalidMessage)
		}
		partType := sourcePart.Type()
		if !validPartType(partType) {
			return nil, "", fmt.Errorf("%w: formatter returned an invalid part type", ErrInvalidMessage)
		}
		if markup, ok := sourcePart.(interface {
			Kind() string
			Name() string
		}); ok {
			if descriptor.Output != OutputRich || !slices.Contains(descriptor.Markup, markup.Name()) {
				return nil, "", fmt.Errorf("%w: formatter returned forbidden markup", ErrInvalidMessage)
			}
			kind := PartMarkupStandalone
			switch markup.Kind() {
			case "open":
				kind = PartMarkupOpen
			case "close":
				kind = PartMarkupClose
			case "standalone":
			default:
				return nil, "", fmt.Errorf("%w: formatter returned unknown markup", ErrInvalidMessage)
			}
			if nodes >= v.snapshot.limits.MaxOutputParts {
				return nil, "", fmt.Errorf("%w: output has too many parts", ErrLimitExceeded)
			}
			id := messagePartID(sourcePart)
			if id != "" && !validUniversalID(id, v.snapshot.limits.MaxIdentifierBytes) {
				return nil, "", fmt.Errorf("%w: formatter returned an invalid part identifier", ErrInvalidMessage)
			}
			nodes++
			parts = append(parts, Part{Kind: kind, Type: partType, Name: markup.Name(), ID: id})
			continue
		}
		if partType == "bidiIsolation" && v.presentation == PresentationNoIsolation {
			continue
		}
		raw := sourcePart.Value()
		value, ok := raw.(string)
		if !ok {
			if internal, internalOK := raw.(messagevalue.MessageValue); internalOK {
				formatted, formatErr := internal.ToString()
				if formatErr != nil {
					return nil, "", formatErr
				}
				value = formatted
			} else {
				value = fmt.Sprint(raw)
			}
		}
		kind := PartValue
		switch partType {
		case "text":
			kind = PartText
		case "bidiIsolation":
			kind = PartBidiIsolation
		}
		subparts, err := v.convertSubparts(ctx, sourcePart, value)
		if err != nil {
			return nil, "", err
		}
		if len(subparts) > v.snapshot.limits.MaxOutputParts-nodes-1 {
			return nil, "", fmt.Errorf("%w: output has too many parts", ErrLimitExceeded)
		}
		id := messagePartID(sourcePart)
		if id != "" && !validUniversalID(id, v.snapshot.limits.MaxIdentifierBytes) {
			return nil, "", fmt.Errorf("%w: formatter returned an invalid part identifier", ErrInvalidMessage)
		}
		if text.Len()+len(value) > v.snapshot.limits.MaxOutputBytes {
			return nil, "", fmt.Errorf("%w: output exceeds %d bytes", ErrLimitExceeded, v.snapshot.limits.MaxOutputBytes)
		}
		text.WriteString(value)
		nodes += 1 + len(subparts)
		parts = append(parts, Part{Kind: kind, Type: partType, Text: value, ID: id, Locale: sourcePart.Locale(), Direction: string(sourcePart.Dir()), Subparts: subparts})
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return parts, text.String(), nil
}

func (v *View) convertSubparts(ctx context.Context, sourcePart messagevalue.MessagePart, value string) ([]Subpart, error) {
	nested, ok := sourcePart.(interface {
		Parts() []messagevalue.MessagePart
	})
	if !ok {
		return nil, nil
	}
	source := nested.Parts()
	if source == nil {
		return nil, nil
	}
	if len(source) > v.snapshot.limits.MaxOutputParts {
		return nil, fmt.Errorf("%w: output has too many subparts", ErrLimitExceeded)
	}
	converted := make([]Subpart, len(source))
	var joined strings.Builder
	joined.Grow(len(value))
	for index, part := range source {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if part == nil {
			return nil, fmt.Errorf("%w: formatter returned an invalid subpart", ErrInvalidMessage)
		}
		partType := part.Type()
		if !validPartType(partType) {
			return nil, fmt.Errorf("%w: formatter returned an invalid subpart", ErrInvalidMessage)
		}
		text, ok := part.Value().(string)
		if !ok {
			return nil, fmt.Errorf("%w: formatter returned a non-text subpart", ErrInvalidMessage)
		}
		if joined.Len()+len(text) > len(value) {
			return nil, fmt.Errorf("%w: formatter subparts do not match their value", ErrInvalidMessage)
		}
		joined.WriteString(text)
		converted[index] = Subpart{Type: partType, Text: text}
	}
	if joined.String() != value {
		return nil, fmt.Errorf("%w: formatter subparts do not match their value", ErrInvalidMessage)
	}
	return converted, nil
}

func validPartType(value string) bool {
	return validUniversalID(value, maximumPartTypeBytes)
}

func messagePartID(part messagevalue.MessagePart) string {
	identified, ok := part.(interface{ ID() string })
	if !ok {
		return ""
	}
	return identified.ID()
}

type partMetadata struct {
	id        string
	direction bidi.Direction
}

func metadataFromFunctionContext(ctx functions.MessageFunctionContext) partMetadata {
	metadata := partMetadata{id: ctx.ID()}
	if direction := ctx.Dir(); direction != "" {
		metadata.direction = bidi.ParseDirection(direction)
	}
	return metadata
}

func metadataFromFunctionOperand(ctx functions.MessageFunctionContext, direction bidi.Direction) partMetadata {
	metadata := metadataFromFunctionContext(ctx)
	if ctx.Dir() == "" {
		metadata.direction = direction
	}
	return metadata
}

func (m partMetadata) dir(fallback bidi.Direction) bidi.Direction {
	if m.direction != "" {
		return m.direction
	}
	return fallback
}

func nullWithFunctionMetadata(ctx functions.MessageFunctionContext, value *nullMessageValue) *nullMessageValue {
	clone := *value
	clone.source = ctx.Source()
	clone.metadata = metadataFromFunctionOperand(ctx, value.Dir())
	return &clone
}

func resolveFunctionOperand(operand any) (any, error) {
	value, ok := operand.(messagevalue.Valuer)
	if !ok {
		return operand, nil
	}
	return value.ValueOf()
}

type nullMessageValue struct {
	source   string
	locale   string
	budget   *renderBudget
	metadata partMetadata
}

func (v *nullMessageValue) Type() string        { return "null" }
func (v *nullMessageValue) Source() string      { return v.source }
func (v *nullMessageValue) Dir() bidi.Direction { return v.metadata.dir(bidi.DirAuto) }
func (v *nullMessageValue) Locale() string      { return v.locale }
func (v *nullMessageValue) ToString() (string, error) {
	if err := v.budget.charge(0, 1); err != nil {
		return "", err
	}
	return "", nil
}
func (v *nullMessageValue) ValueOf() (any, error) {
	clone := *v
	return &clone, nil
}
func (v *nullMessageValue) ToParts() ([]messagevalue.MessagePart, error) {
	if err := v.budget.charge(0, 1); err != nil {
		return nil, err
	}
	return []messagevalue.MessagePart{formattedMessagePart{kind: "null", source: v.source, locale: v.locale, dir: v.Dir(), id: v.metadata.id}}, nil
}
func (v *nullMessageValue) SelectKeys(keys []string) ([]string, error) {
	if err := v.budget.require(0, 0); err != nil {
		return nil, err
	}
	if slices.Contains(keys, "null") {
		return []string{"null"}, nil
	}
	return nil, nil
}

type renderBudget struct {
	bytes     int
	parts     int
	exhausted bool
}

func (b *renderBudget) require(bytes, parts int) error {
	if b == nil {
		return nil
	}
	if b.exhausted || bytes < 0 || parts < 0 || bytes > b.bytes || parts > b.parts {
		b.exhausted = true
		return ErrLimitExceeded
	}
	return nil
}

func (b *renderBudget) charge(bytes, parts int) error {
	if b == nil {
		return nil
	}
	if b.exhausted || bytes < 0 || parts < 0 || bytes > b.bytes || parts > b.parts {
		b.exhausted = true
		return ErrLimitExceeded
	}
	b.bytes -= bytes
	b.parts -= parts
	return nil
}

type boundedStringValue struct {
	value    string
	locale   string
	source   string
	budget   *renderBudget
	metadata partMetadata
}

func (v *boundedStringValue) Type() string        { return "string" }
func (v *boundedStringValue) Source() string      { return v.source }
func (v *boundedStringValue) Dir() bidi.Direction { return v.metadata.dir(bidi.DirAuto) }
func (v *boundedStringValue) Locale() string      { return v.locale }
func (v *boundedStringValue) ToString() (string, error) {
	if err := v.budget.charge(len(v.value), 1); err != nil {
		return "", err
	}
	return v.value, nil
}
func (v *boundedStringValue) ToParts() ([]messagevalue.MessagePart, error) {
	if err := v.budget.charge(len(v.value), 1); err != nil {
		return nil, err
	}
	return []messagevalue.MessagePart{formattedMessagePart{kind: "string", value: v.value, source: v.source, locale: v.locale, dir: v.Dir(), id: v.metadata.id}}, nil
}
func (v *boundedStringValue) ValueOf() (any, error) {
	clone := *v
	return &clone, nil
}
func (v *boundedStringValue) SelectKeys(keys []string) ([]string, error) {
	if err := v.budget.require(0, 0); err != nil {
		return nil, err
	}
	compare := norm.NFC.String(v.value)
	for _, key := range keys {
		if key == compare {
			return []string{key}, nil
		}
	}
	return nil, nil
}

func newBigInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}
