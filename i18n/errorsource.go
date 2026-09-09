package i18n

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/frostgrove/vv/errs"
)

type ErrorParam struct {
	Param    string
	Argument string
	Currency string
}

type ErrorMapping struct {
	Ladder        string
	Key           Key
	Params        []ErrorParam
	FieldArgument string
}

type FieldLabel struct {
	Field string
	Key   Key
}

type ErrorSpec struct {
	Mappings         []ErrorMapping
	FieldLabels      []FieldLabel
	FormattingLocale string
	TimeZone         string
	Presentation     Presentation
}

type ErrorPlanSpec struct {
	Mappings    []ErrorMapping
	FieldLabels []FieldLabel
}

type LocalizedMessageSource interface {
	errs.MessageSource
	MessageWithLocale(ctx context.Context, violation errs.Violation, locale string) (message, actualLocale string, ok bool)
}

type compiledErrorMapping struct {
	key           Key
	params        []compiledErrorParam
	fieldArgument string
}

type compiledErrorParam struct {
	param    string
	argument ArgumentSpec
	currency string
}

type ErrorPlan struct {
	snapshot    *Snapshot
	mappings    map[string]compiledErrorMapping
	fieldLabels map[string]Key
}

type errorMessageSource struct {
	plan             *ErrorPlan
	view             *View
	formattingLocale string
	timeZone         string
	presentation     Presentation
}

var _ LocalizedMessageSource = (*errorMessageSource)(nil)

type errorTerminal struct {
	outcome Outcome
	reason  Reason
	rank    int
}

func newErrorTerminal() errorTerminal {
	return errorTerminal{outcome: OutcomeMissing, reason: ReasonMissingTemplate, rank: 1}
}

func (t *errorTerminal) record(outcome Outcome, reason Reason) {
	rank := errorTerminalRank(outcome, reason)
	if rank > t.rank {
		t.outcome = outcome
		t.reason = reason
		t.rank = rank
	}
}

func errorTerminalRank(outcome Outcome, reason Reason) int {
	switch {
	case outcome == OutcomeTimedOut && reason == ReasonContextDeadline:
		return 6
	case outcome == OutcomeCanceled && reason == ReasonContextCanceled:
		return 5
	case outcome == OutcomeLimited && (reason == ReasonOutputLimit || reason == ReasonLimit):
		return 4
	case outcome == OutcomeInvalid && (reason == ReasonTemplateFailure || reason == ReasonSchemaMismatch):
		return 3
	case outcome == OutcomeInvalid && reason == ReasonInvalidArgument:
		return 2
	case outcome == OutcomeMissing && reason == ReasonMissingTemplate:
		return 1
	default:
		return 0
	}
}

func safeRenderTerminal(outcome Outcome, reason Reason) (Outcome, Reason) {
	if errorTerminalRank(outcome, reason) > 0 {
		return outcome, reason
	}
	return OutcomeInvalid, ReasonTemplateFailure
}

func (s *Snapshot) ErrorMessages(spec ErrorSpec) (LocalizedMessageSource, error) {
	if s == nil || s.resolver == nil {
		return nil, fmt.Errorf("%w: snapshot is nil", ErrInvalidCatalog)
	}
	if spec.Presentation != PresentationDefault && spec.Presentation != PresentationNoIsolation {
		return nil, fmt.Errorf("%w: error presentation is invalid", ErrInvalidMessage)
	}
	formattingLocale := spec.FormattingLocale
	if formattingLocale != "" {
		_, canonical, err := canonicalLocale(formattingLocale, s.limits.Locale.MaxTagBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: formatting locale: %v", ErrInvalidLocale, err)
		}
		formattingLocale = canonical
	}
	timeZone := spec.TimeZone
	if timeZone == "" {
		timeZone = s.defaultTimeZone
	}
	canonicalZone, err := canonicalTimeZone(timeZone)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if canonicalZone != "UTC" && s.timeZoneDataVersion == "" {
		return nil, fmt.Errorf("%w: non-UTC formatting requires a time-zone data version", ErrInvalidMessage)
	}
	plan, err := s.compileErrorPlan(ErrorPlanSpec{Mappings: spec.Mappings, FieldLabels: spec.FieldLabels}, spec.FormattingLocale, spec.TimeZone)
	if err != nil {
		return nil, err
	}
	if formattingLocale != "" {
		if err := validateErrorFormattingLocale(formattingLocale, plan); err != nil {
			return nil, err
		}
	}
	return &errorMessageSource{
		plan:             plan,
		formattingLocale: formattingLocale,
		timeZone:         canonicalZone,
		presentation:     spec.Presentation,
	}, nil
}

func (s *Snapshot) ErrorPlan(spec ErrorPlanSpec) (*ErrorPlan, error) {
	if s == nil || s.resolver == nil {
		return nil, fmt.Errorf("%w: snapshot is nil", ErrInvalidCatalog)
	}
	return s.compileErrorPlan(spec)
}

func (v *View) ErrorMessages(plan *ErrorPlan) (LocalizedMessageSource, error) {
	if v == nil || v.snapshot == nil {
		return nil, fmt.Errorf("%w: view is nil", ErrInvalidCatalog)
	}
	if plan == nil || plan.snapshot == nil {
		return nil, fmt.Errorf("%w: error plan is nil", ErrInvalidCatalog)
	}
	if plan.snapshot != v.snapshot {
		return nil, fmt.Errorf("%w: error plan belongs to another snapshot", ErrInvalidCatalog)
	}
	return &errorMessageSource{plan: plan, view: v}, nil
}

func (s *Snapshot) compileErrorPlan(spec ErrorPlanSpec, extraMaterial ...string) (*ErrorPlan, error) {
	problems := &problemSet{}
	if len(spec.Mappings) > s.limits.MaxMessages {
		problems.add(ProblemLimit, "error.mappings", fmt.Sprint(len(spec.Mappings)))
	}
	if len(spec.FieldLabels) > s.limits.MaxMessages {
		problems.add(ProblemLimit, "error.field_labels", fmt.Sprint(len(spec.FieldLabels)))
	}
	if err := problems.err(); err != nil {
		return nil, err
	}
	counter := artifactCounter{maxBytes: s.limits.MaxCatalogBytes, maxItems: s.limits.MaxCatalogItems}
	if !counter.add(extraMaterial...) {
		problems.add(ProblemLimit, "error", "error mapping material exceeds configured bounds")
		return nil, problems.err()
	}
	for i, mapping := range spec.Mappings {
		if len(mapping.Params) > s.limits.MaxArguments {
			problems.add(ProblemLimit, problemPath("error.mappings[%d].params", i), fmt.Sprint(len(mapping.Params)))
			return nil, problems.err()
		}
		if !counter.add(mapping.Ladder, string(mapping.Key), mapping.FieldArgument) {
			problems.add(ProblemLimit, problemPath("error.mappings[%d]", i), "error mapping material exceeds configured bounds")
			return nil, problems.err()
		}
		for paramIndex, param := range mapping.Params {
			if !counter.add(param.Param, param.Argument, param.Currency) {
				problems.add(ProblemLimit, problemPath("error.mappings[%d].params[%d]", i, paramIndex), "error mapping material exceeds configured bounds")
				return nil, problems.err()
			}
		}
	}
	for i, label := range spec.FieldLabels {
		if !counter.add(label.Field, string(label.Key)) {
			problems.add(ProblemLimit, problemPath("error.field_labels[%d]", i), "error mapping material exceeds configured bounds")
			return nil, problems.err()
		}
	}
	mappings := make(map[string]compiledErrorMapping, len(spec.Mappings))
	for i, mapping := range spec.Mappings {
		path := problemPath("error.mappings[%d]", i)
		if !validLadderKey(mapping.Ladder, s.limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, path+".ladder", mapping.Ladder)
		}
		if _, exists := mappings[mapping.Ladder]; exists {
			problems.add(ProblemDuplicate, path+".ladder", mapping.Ladder)
			continue
		}
		record, ok := s.records[mapping.Key]
		if !ok {
			problems.add(ProblemMissing, path+".key", string(mapping.Key))
			continue
		}
		bindings := validateErrorParams(mapping, record.descriptor, s.limits, path, problems)
		mappings[mapping.Ladder] = compiledErrorMapping{key: mapping.Key, params: bindings, fieldArgument: mapping.FieldArgument}
	}
	fieldLabels := make(map[string]Key, len(spec.FieldLabels))
	for i, label := range spec.FieldLabels {
		path := problemPath("error.field_labels[%d]", i)
		if !validIdentifier(label.Field, s.limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, path+".field", label.Field)
		}
		if _, exists := fieldLabels[label.Field]; exists {
			problems.add(ProblemDuplicate, path+".field", label.Field)
			continue
		}
		record, ok := s.records[label.Key]
		if !ok {
			problems.add(ProblemMissing, path+".key", string(label.Key))
			continue
		}
		if len(record.descriptor.Arguments) != 0 || record.descriptor.Output != OutputPlain {
			problems.add(ProblemSchema, path+".key", "field labels must be argument-free plain messages")
			continue
		}
		fieldLabels[label.Field] = label.Key
	}
	if err := problems.err(); err != nil {
		return nil, err
	}
	return &ErrorPlan{snapshot: s, mappings: mappings, fieldLabels: fieldLabels}, nil
}

func validateErrorFormattingLocale(formattingLocale string, plan *ErrorPlan) error {
	var requirements formatRequirements
	keys := make(map[Key]bool, len(plan.mappings)+len(plan.fieldLabels))
	for _, mapping := range plan.mappings {
		keys[mapping.key] = true
	}
	for _, key := range plan.fieldLabels {
		keys[key] = true
	}
	for key := range keys {
		for _, translation := range plan.snapshot.records[key].templates {
			requirements |= translation.work.requirements & (formatNumber | formatDate)
		}
	}
	if err := validateFormatRequirements(formattingLocale, requirements); err != nil {
		return fmt.Errorf("%w: formatting locale: %v", ErrInvalidLocale, err)
	}
	return nil
}

func validateErrorParams(mapping ErrorMapping, descriptor Descriptor, limits Limits, path string, problems *problemSet) []compiledErrorParam {
	byArgument := make(map[string]ArgumentSpec, len(descriptor.Arguments))
	for _, argument := range descriptor.Arguments {
		byArgument[argument.Name] = argument
	}
	provided := make(map[string]bool, len(mapping.Params)+1)
	seenParams := make(map[string]bool, len(mapping.Params))
	out := make([]compiledErrorParam, len(mapping.Params))
	for i, binding := range mapping.Params {
		bindingPath := problemPath("%s.params[%d]", path, i)
		if !validIdentifier(binding.Param, limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, bindingPath+".param", binding.Param)
		}
		if seenParams[binding.Param] {
			problems.add(ProblemDuplicate, bindingPath+".param", binding.Param)
		}
		seenParams[binding.Param] = true
		argument, ok := byArgument[binding.Argument]
		if !ok {
			problems.add(ProblemSchema, bindingPath+".argument", "argument is not declared")
			continue
		}
		out[i] = compiledErrorParam{param: binding.Param, argument: argument, currency: binding.Currency}
		if provided[binding.Argument] {
			problems.add(ProblemDuplicate, bindingPath+".argument", binding.Argument)
		}
		provided[binding.Argument] = true
		if binding.Currency != "" && (argument.Type != TypeMoney || !validCurrency(binding.Currency)) {
			problems.add(ProblemSchema, bindingPath+".currency", "currency is only valid for Money and must have three letters")
		}
		if argument.Type == TypeMoney && binding.Currency == "" {
			problems.add(ProblemMissing, bindingPath+".currency", "Money parameters require a declared currency")
		}
	}
	if mapping.FieldArgument != "" {
		argument, ok := byArgument[mapping.FieldArgument]
		if !ok || argument.Type != TypeText {
			problems.add(ProblemSchema, path+".field_argument", "field label argument must be declared as text")
		} else if provided[mapping.FieldArgument] {
			problems.add(ProblemDuplicate, path+".field_argument", mapping.FieldArgument)
		} else {
			provided[mapping.FieldArgument] = true
		}
	}
	for _, argument := range descriptor.Arguments {
		if argument.Required && !provided[argument.Name] {
			problems.add(ProblemMissing, path+".params", "required argument "+argument.Name+" has no source")
		}
	}
	return out
}

func (s *errorMessageSource) Message(ctx context.Context, violation errs.Violation, requestedLocale string) (string, bool) {
	text, _, ok := s.MessageWithLocale(ctx, violation, requestedLocale)
	return text, ok
}

func (s *errorMessageSource) MessageWithLocale(ctx context.Context, violation errs.Violation, requestedLocale string) (text, actualLocale string, ok bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	terminal := newErrorTerminal()
	outcome := terminal.outcome
	reason := terminal.reason
	defer func() {
		if recover() != nil {
			text, actualLocale, ok = "", "", false
			outcome, reason = OutcomeInvalid, ReasonTemplateFailure
		}
		if s != nil && s.plan != nil && s.plan.snapshot != nil {
			notifyObserver(ctx, s.plan.snapshot.observer, Observation{Operation: OperationRender, Outcome: outcome, Reason: reason, Duration: time.Since(started), Count: 1})
		}
	}()
	if s == nil || s.plan == nil || s.plan.snapshot == nil {
		return "", "", false
	}
	snapshot := s.plan.snapshot
	var resolution Resolution
	view := s.view
	if view != nil {
		resolution = view.resolution
	} else {
		resolver := *snapshot.resolver
		resolver.observer = nil
		if requestedLocale == "" {
			resolution = resolver.ResolveContext(ctx)
		} else {
			resolution = resolver.ResolveContext(ctx, Exact(SourceProtocol, requestedLocale))
		}
		resolution.owner = snapshot.resolver
		if !resolution.Matched() {
			outcome, reason = resolution.Outcome, resolution.Reason
			return "", "", false
		}
		var err error
		view, err = snapshot.View(ViewSpec{Resolution: resolution, FormattingLocale: s.formattingLocale, TimeZone: s.timeZone, Presentation: s.presentation})
		if err != nil {
			outcome, reason = OutcomeInvalid, ReasonInvalidArgument
			return "", "", false
		}
	}
	ladders := errorLadder(violation, snapshot.limits.MaxIdentifierBytes, snapshot.limits.MaxExplainSteps)
	if len(ladders) == 0 {
		outcome, reason = OutcomeInvalid, ReasonInvalidArgument
		return "", "", false
	}
	for localeIndex, templateLocale := range snapshot.fallbackChain(resolution.Locale) {
		if localeIndex&7 == 0 {
			if err := ctx.Err(); err != nil {
				outcome, reason = contextOperationResult(err)
				return "", "", false
			}
		}
		for ladderIndex, ladder := range ladders {
			if ladderIndex&7 == 0 {
				if err := ctx.Err(); err != nil {
					outcome, reason = contextOperationResult(err)
					return "", "", false
				}
			}
			mapping, exists := s.plan.mappings[ladder]
			if !exists {
				continue
			}
			record := snapshot.records[mapping.key]
			translation, exists := record.templates[templateLocale]
			if !exists {
				continue
			}
			arguments, terminalOutcome, terminalReason, valid := s.mappingArguments(ctx, view, mapping, violation, templateLocale)
			if !valid {
				terminal.record(terminalOutcome, terminalReason)
				continue
			}
			message, err := snapshot.Bind(mapping.key, arguments...)
			if err != nil {
				terminal.record(OutcomeInvalid, ReasonInvalidArgument)
				continue
			}
			rendered, renderOutcome, renderReason, err := view.renderTranslation(ctx, message, record, translation, arguments)
			if err != nil {
				if contextErr := ctx.Err(); contextErr != nil {
					outcome, reason = contextOperationResult(contextErr)
					return "", "", false
				}
				renderOutcome, renderReason = safeRenderTerminal(renderOutcome, renderReason)
				terminal.record(renderOutcome, renderReason)
				continue
			}
			outcome, reason = renderOutcome, renderReason
			if renderOutcome == OutcomeSuccess && resolution.Outcome != OutcomeSuccess {
				outcome, reason = resolution.Outcome, resolution.Reason
			}
			return rendered.Text, rendered.TemplateLocale, true
		}
	}
	outcome, reason = terminal.outcome, terminal.reason
	return "", "", false
}

func (s *errorMessageSource) mappingArguments(ctx context.Context, view *View, mapping compiledErrorMapping, violation errs.Violation, templateLocale string) ([]Argument, Outcome, Reason, bool) {
	arguments := make([]Argument, 0, len(mapping.params)+1)
	for _, binding := range mapping.params {
		value, exists := violation.Params[binding.param]
		if !exists {
			continue
		}
		argument, valid := errorArgument(binding.argument.Name, binding.argument, binding.currency, value)
		if !valid {
			return nil, OutcomeInvalid, ReasonInvalidArgument, false
		}
		arguments = append(arguments, argument)
	}
	if mapping.fieldArgument != "" {
		field := lastNamedField(violation.Path)
		labelKey, exists := s.plan.fieldLabels[field]
		if !exists {
			return nil, OutcomeMissing, ReasonMissingTemplate, false
		}
		labelMessage, err := s.plan.snapshot.Bind(labelKey)
		if err != nil {
			return nil, OutcomeInvalid, ReasonInvalidArgument, false
		}
		labelRecord := s.plan.snapshot.records[labelKey]
		labelTranslation, exists := labelRecord.templates[templateLocale]
		if !exists {
			return nil, OutcomeMissing, ReasonMissingTemplate, false
		}
		label, labelOutcome, labelReason, err := view.renderTranslation(ctx, labelMessage, labelRecord, labelTranslation, nil)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				outcome, reason := contextOperationResult(contextErr)
				return nil, outcome, reason, false
			}
			labelOutcome, labelReason = safeRenderTerminal(labelOutcome, labelReason)
			return nil, labelOutcome, labelReason, false
		}
		arguments = append(arguments, Text(mapping.fieldArgument, label.Text))
	}
	return arguments, OutcomeSuccess, ReasonExact, true
}

func errorArgument(name string, spec ArgumentSpec, currency string, value any) (Argument, bool) {
	if value == nil {
		return Null(name), spec.Nullable
	}
	switch spec.Type {
	case TypeText:
		text, ok := value.(string)
		return Text(name, text), ok
	case TypeBool:
		boolean, ok := value.(bool)
		return Bool(name, boolean), ok
	case TypeInteger:
		integer, ok := signedParam(value)
		return Integer(name, integer), ok
	case TypeUnsignedInteger:
		integer, ok := unsignedParam(value)
		return UnsignedInteger(name, integer), ok
	case TypeBigInteger:
		switch integer := value.(type) {
		case big.Int:
			return BigInteger(name, &integer), true
		case *big.Int:
			if integer != nil {
				return BigInteger(name, integer), true
			}
		}
		return Argument{}, false
	case TypeDecimal:
		lexical, ok := value.(string)
		return Decimal(name, lexical), ok && validateDecimal(lexical) == nil
	case TypeMoney:
		lexical, ok := value.(string)
		argument := Money(name, lexical, currency)
		return argument, ok && argument.invalid == ""
	case TypeDate:
		switch date := value.(type) {
		case DateValue:
			argument := Date(name, date.Year, date.Month, date.Day)
			return argument, argument.invalid == ""
		case time.Time:
			argument := Date(name, date.Year(), date.Month(), date.Day())
			return argument, argument.invalid == ""
		}
		return Argument{}, false
	case TypeInstant:
		instant, ok := value.(time.Time)
		argument := Instant(name, instant)
		return argument, ok && argument.invalid == ""
	case TypeEnum:
		text, ok := value.(string)
		return Enum(name, text), ok && slices.Contains(spec.Values, text)
	default:
		return Argument{}, false
	}
}

func signedParam(value any) (int64, bool) {
	switch integer := value.(type) {
	case int:
		return int64(integer), true
	case int8:
		return int64(integer), true
	case int16:
		return int64(integer), true
	case int32:
		return int64(integer), true
	case int64:
		return integer, true
	}
	return 0, false
}

func unsignedParam(value any) (uint64, bool) {
	switch integer := value.(type) {
	case uint:
		return uint64(integer), true
	case uint8:
		return uint64(integer), true
	case uint16:
		return uint64(integer), true
	case uint32:
		return uint64(integer), true
	case uint64:
		return integer, true
	}
	return 0, false
}

func errorLadder(violation errs.Violation, maxIdentifierBytes, maxSteps int) []string {
	code := string(violation.Code)
	if code == "" || len(code) > maxIdentifierBytes || len(violation.Path) > maxSteps || !utf8.ValidString(code) || hasUnsafeBidiControls(code) {
		return nil
	}
	first, last := "", ""
	for _, step := range violation.Path {
		if step.IsIndex {
			continue
		}
		if step.Name == "" || len(step.Name) > maxIdentifierBytes || !utf8.ValidString(step.Name) || hasUnsafeBidiControls(step.Name) {
			return nil
		}
		if first == "" {
			first = step.Name
		}
		last = step.Name
	}
	keys := make([]string, 0, 4)
	add := func(key string) {
		if key != "" && (len(keys) == 0 || keys[len(keys)-1] != key) {
			keys = append(keys, key)
		}
	}
	if first != "" && last != "" && first != last {
		add(first + "." + last + "." + code)
	}
	if first != "" {
		add(first + "." + code)
	}
	if last != "" {
		add(last + "." + code)
	}
	add(code)
	return keys
}

func lastNamedField(path errs.Path) string {
	for i := len(path) - 1; i >= 0; i-- {
		if !path[i].IsIndex {
			return path[i].Name
		}
	}
	return ""
}

func validLadderKey(value string, maxIdentifierBytes int) bool {
	if value == "" || len(value) > maxIdentifierBytes*4 || !utf8.ValidString(value) || hasUnsafeBidiControls(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
