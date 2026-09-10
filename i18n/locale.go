package i18n

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"
)

type ChoiceSource uint8

const (
	SourceExplicit ChoiceSource = iota + 1
	SourceUser
	SourceProtocol
	SourceTenant
	SourceApplication
)

func (s ChoiceSource) String() string {
	switch s {
	case SourceExplicit:
		return "explicit"
	case SourceUser:
		return "user"
	case SourceProtocol:
		return "protocol"
	case SourceTenant:
		return "tenant"
	case SourceApplication:
		return "application"
	default:
		return "unknown"
	}
}

func (s ChoiceSource) Valid() bool { return validChoiceSource(s) }

type MatchMode uint8

const (
	MatchLookup MatchMode = iota
	MatchBestFit
)

func (m MatchMode) String() string {
	switch m {
	case MatchLookup:
		return "lookup"
	case MatchBestFit:
		return "best_fit"
	default:
		return "unknown"
	}
}

func (m MatchMode) Valid() bool { return m == MatchLookup || m == MatchBestFit }

type LocaleLimits struct {
	MaxChoices       int
	MaxHeaderBytes   int
	MaxRanges        int
	MaxSupported     int
	MaxTagBytes      int
	MaxFallbackDepth int
}

type LocaleEdge struct {
	Locale string
	Parent string
}

type LocalePolicy struct {
	Supported     []string
	Default       string
	Parents       []LocaleEdge
	Mode          MatchMode
	DefaultOnMiss bool
	Limits        LocaleLimits
	Observer      Observer
}

type choiceKind uint8

const (
	choiceExact choiceKind = iota + 1
	choiceAcceptLanguage
)

const (
	maxAcceptLanguageBytes  = 65536
	maxAcceptLanguageRanges = 256
)

type Choice struct {
	kind        choiceKind
	source      ChoiceSource
	values      []string
	headerBytes int
	ranges      int
	limited     bool
}

func Exact(source ChoiceSource, locale string) Choice {
	return Choice{kind: choiceExact, source: source, values: []string{locale}}
}

func AcceptLanguage(source ChoiceSource, values ...string) Choice {
	choice := Choice{kind: choiceAcceptLanguage, source: source}
	if len(values) > maxAcceptLanguageRanges {
		choice.limited = true
		return choice
	}
	for index, value := range values {
		if index != 0 {
			if choice.headerBytes == maxAcceptLanguageBytes {
				choice.limited = true
				return choice
			}
			choice.headerBytes++
		}
		if len(value) > maxAcceptLanguageBytes-choice.headerBytes {
			choice.limited = true
			return choice
		}
		choice.headerBytes += len(value)
		choice.ranges++
		for index := 0; index < len(value); index++ {
			if value[index] == ',' {
				choice.ranges++
				if choice.ranges > maxAcceptLanguageRanges {
					choice.limited = true
					return choice
				}
			}
		}
	}
	choice.values = slices.Clone(values)
	return choice
}

type PreferenceStep struct {
	Source  ChoiceSource
	Outcome Outcome
	Reason  Reason
}

type Resolution struct {
	Locale          string
	Source          ChoiceSource
	Outcome         Outcome
	Reason          Reason
	owner           *Resolver
	seal            resolutionSeal
	preferenceSteps []PreferenceStep
}

type resolutionIssuer struct {
	marker byte
}

type resolutionSeal struct {
	issuer  *resolutionIssuer
	locale  string
	source  ChoiceSource
	outcome Outcome
	reason  Reason
}

func (r Resolution) Matched() bool {
	return r.Locale != "" && (r.Outcome == OutcomeSuccess || r.Outcome == OutcomeFallback || r.Outcome == OutcomeDefault)
}

type Resolver struct {
	supported     []string
	tags          []language.Tag
	index         map[string]int
	defaultLocale string
	parents       map[string]string
	mode          MatchMode
	defaultOnMiss bool
	limits        LocaleLimits
	matcher       language.Matcher
	observer      Observer
	issuer        *resolutionIssuer
}

func NewResolver(policy LocalePolicy) (*Resolver, error) {
	limits, err := checkedLocaleLimits(policy.Limits)
	if err != nil {
		return nil, err
	}
	if len(policy.Supported) == 0 {
		return nil, fmt.Errorf("%w: supported locales are empty", ErrInvalidLocale)
	}
	if len(policy.Supported) > limits.MaxSupported {
		return nil, fmt.Errorf("%w: supported locales exceed %d", ErrInvalidLocale, limits.MaxSupported)
	}
	if len(policy.Parents) > limits.MaxSupported {
		return nil, fmt.Errorf("%w: parent edges exceed %d", ErrInvalidLocale, limits.MaxSupported)
	}
	if policy.Mode != MatchLookup && policy.Mode != MatchBestFit {
		return nil, fmt.Errorf("%w: unknown matching mode", ErrInvalidLocale)
	}

	supported := make([]string, 0, len(policy.Supported))
	tags := make([]language.Tag, 0, len(policy.Supported))
	index := make(map[string]int, len(policy.Supported))
	for i, raw := range policy.Supported {
		tag, canonical, err := canonicalLocale(raw, limits.MaxTagBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: supported[%d]: %v", ErrInvalidLocale, i, err)
		}
		if previous, exists := index[canonical]; exists {
			return nil, fmt.Errorf("%w: supported[%d] collides with supported[%d] as %q", ErrInvalidLocale, i, previous, canonical)
		}
		index[canonical] = i
		supported = append(supported, canonical)
		tags = append(tags, tag)
	}

	_, defaultLocale, err := canonicalLocale(policy.Default, limits.MaxTagBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: default: %v", ErrInvalidLocale, err)
	}
	if _, ok := index[defaultLocale]; !ok {
		return nil, fmt.Errorf("%w: default %q is not supported", ErrInvalidLocale, defaultLocale)
	}

	parents := make(map[string]string, len(policy.Parents))
	for i, edge := range policy.Parents {
		_, child, childErr := canonicalLocale(edge.Locale, limits.MaxTagBytes)
		_, parent, parentErr := canonicalLocale(edge.Parent, limits.MaxTagBytes)
		if childErr != nil || parentErr != nil {
			return nil, fmt.Errorf("%w: parents[%d] contains an invalid locale", ErrInvalidLocale, i)
		}
		if child == parent {
			return nil, fmt.Errorf("%w: parent edge %q points to itself", ErrInvalidLocale, child)
		}
		if previous, exists := parents[child]; exists && previous != parent {
			return nil, fmt.Errorf("%w: locale %q has two parents", ErrInvalidLocale, child)
		}
		parents[child] = parent
	}
	seeds := slices.Clone(supported)
	for child, parent := range parents {
		seeds = append(seeds, child, parent)
	}
	cycle, tooDeep := lookupLocaleGraphProblem(seeds, parents, limits.MaxFallbackDepth)
	if cycle != "" {
		return nil, fmt.Errorf("%w: fallback cycle at %q", ErrInvalidLocale, cycle)
	}
	if tooDeep != "" {
		return nil, fmt.Errorf("%w: fallback chain from %q exceeds %d", ErrInvalidLocale, tooDeep, limits.MaxFallbackDepth)
	}

	return &Resolver{
		supported:     supported,
		tags:          tags,
		index:         index,
		defaultLocale: defaultLocale,
		parents:       parents,
		mode:          policy.Mode,
		defaultOnMiss: policy.DefaultOnMiss,
		limits:        limits,
		matcher:       language.NewMatcher(tags),
		observer:      policy.Observer,
		issuer:        &resolutionIssuer{marker: 1},
	}, nil
}

func (r *Resolver) Supported() []string {
	if r == nil {
		return nil
	}
	return slices.Clone(r.supported)
}

func (r *Resolver) Default() string {
	if r == nil {
		return ""
	}
	return r.defaultLocale
}

func (r *Resolver) Resolve(choices ...Choice) Resolution {
	return r.ResolveContext(context.Background(), choices...)
}

func (r *Resolver) ResolveContext(ctx context.Context, choices ...Choice) (result Resolution) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	steps := make([]PreferenceStep, 0, min(len(choices), 16))
	recordStep := func(source ChoiceSource, outcome Outcome, reason Reason) {
		steps = append(steps, PreferenceStep{Source: source, Outcome: outcome, Reason: reason})
	}
	result = Resolution{Outcome: OutcomeNoMatch, Reason: ReasonUnsupported}
	if r == nil {
		result.Outcome = OutcomeInvalid
		result.Reason = ReasonMalformed
		return result
	}
	defer func() {
		result.preferenceSteps = slices.Clone(steps)
		r.issue(&result)
		notifyObserver(ctx, r.observer, Observation{Operation: OperationResolve, Outcome: result.Outcome, Reason: result.Reason, Duration: time.Since(started), Count: 1})
	}()
	if err := ctx.Err(); err != nil {
		result.Outcome, result.Reason = contextOperationResult(err)
		return result
	}
	if len(choices) > r.limits.MaxChoices {
		result.Outcome = OutcomeLimited
		result.Reason = ReasonLimit
		return result
	}

	exclusions := make([]language.Tag, 0)
	excludeWildcard := false
	validChoice := false
	for choiceIndex, choice := range choices {
		if choiceIndex&7 == 0 {
			if err := ctx.Err(); err != nil {
				result.Outcome, result.Reason = contextOperationResult(err)
				return result
			}
		}
		if !validChoiceSource(choice.source) {
			result.Outcome = OutcomeInvalid
			result.Reason = ReasonMalformed
			recordStep(choice.source, result.Outcome, result.Reason)
			continue
		}
		switch choice.kind {
		case choiceExact:
			validChoice = true
			if len(choice.values) != 1 || len(choice.values[0]) > r.limits.MaxTagBytes {
				result.Source = choice.source
				result.Outcome = OutcomeLimited
				result.Reason = ReasonLimit
				recordStep(choice.source, result.Outcome, result.Reason)
				continue
			}
			tag, _, err := canonicalLocale(choice.values[0], r.limits.MaxTagBytes)
			if err != nil {
				result.Source = choice.source
				result.Outcome = OutcomeInvalid
				result.Reason = ReasonMalformed
				recordStep(choice.source, result.Outcome, result.Reason)
				continue
			}
			var allowed func(int) (bool, error)
			if excludeWildcard || len(exclusions) != 0 {
				allowed = func(index int) (bool, error) {
					excluded, exclusionErr := matchesAnyContext(ctx, r.tags[index], exclusions)
					return !excludeWildcard && !excluded, exclusionErr
				}
			}
			locale, reason, ok, matchErr := r.match(ctx, tag, allowed)
			if matchErr != nil {
				result.Source = choice.source
				result.Outcome, result.Reason = contextOperationResult(matchErr)
				recordStep(choice.source, result.Outcome, result.Reason)
				return result
			}
			if ok {
				outcome := outcomeForReason(reason)
				recordStep(choice.source, outcome, reason)
				result = Resolution{Locale: locale, Source: choice.source, Outcome: outcome, Reason: reason}
				return result
			}
			result.Source = choice.source
			result.Outcome = OutcomeNoMatch
			if excludeWildcard || len(exclusions) > 0 {
				result.Reason = ReasonExcluded
			} else {
				result.Reason = ReasonUnsupported
			}
			recordStep(choice.source, result.Outcome, result.Reason)
		case choiceAcceptLanguage:
			validChoice = true
			if choice.limited || choice.headerBytes > r.limits.MaxHeaderBytes || choice.ranges > r.limits.MaxRanges {
				result.Source = choice.source
				result.Outcome = OutcomeLimited
				result.Reason = ReasonLimit
				recordStep(choice.source, result.Outcome, result.Reason)
				return result
			}
			locale, reason, excluded, excludesAll, outcome := r.resolveAcceptLanguage(ctx, choice.values, exclusions, excludeWildcard)
			exclusions = append(exclusions, excluded...)
			excludeWildcard = excludeWildcard || excludesAll
			if locale != "" {
				outcome = outcomeForReason(reason)
				recordStep(choice.source, outcome, reason)
				result = Resolution{Locale: locale, Source: choice.source, Outcome: outcome, Reason: reason}
				return result
			}
			result.Source = choice.source
			result.Outcome = outcome
			result.Reason = reason
			recordStep(choice.source, result.Outcome, result.Reason)
			if outcome == OutcomeCanceled || outcome == OutcomeTimedOut || outcome == OutcomeLimited {
				return result
			}
		default:
			result.Outcome = OutcomeInvalid
			result.Reason = ReasonMalformed
			recordStep(choice.source, result.Outcome, result.Reason)
		}
	}

	if err := ctx.Err(); err != nil {
		result.Outcome, result.Reason = contextOperationResult(err)
		return result
	}
	defaultExcluded, err := matchesAnyContext(ctx, r.tags[r.index[r.defaultLocale]], exclusions)
	if err != nil {
		result.Outcome, result.Reason = contextOperationResult(err)
		return result
	}
	defaultableMiss := result.Reason == ReasonUnsupported || result.Reason == ReasonExcluded
	if (len(choices) == 0 || r.defaultOnMiss && validChoice && defaultableMiss) && !excludeWildcard && !defaultExcluded {
		result = Resolution{Locale: r.defaultLocale, Source: SourceApplication, Outcome: OutcomeDefault, Reason: ReasonPolicyDefault}
	}
	return result
}

func (r *Resolver) issue(result *Resolution) {
	result.owner = r
	result.seal = resolutionSeal{
		issuer:  r.issuer,
		locale:  result.Locale,
		source:  result.Source,
		outcome: result.Outcome,
		reason:  result.Reason,
	}
}

func (r *Resolver) owns(resolution Resolution) bool {
	return r != nil && r.issuer != nil && resolution.owner == r &&
		resolution.seal.issuer == r.issuer &&
		resolution.seal.locale == resolution.Locale &&
		resolution.seal.source == resolution.Source &&
		resolution.seal.outcome == resolution.Outcome &&
		resolution.seal.reason == resolution.Reason
}

type weightedRange struct {
	tag      language.Tag
	wildcard bool
	quality  int
	order    int
}

func (r *Resolver) resolveAcceptLanguage(ctx context.Context, values []string, priorExclusions []language.Tag, priorWildcard bool) (string, Reason, []language.Tag, bool, Outcome) {
	ranges := make([]weightedRange, 0, r.limits.MaxRanges)
	excluded := make([]language.Tag, 0)
	excludeWildcard := false
	malformed := false
	order := 0
	for valueIndex, value := range values {
		if valueIndex&7 == 0 {
			if err := ctx.Err(); err != nil {
				outcome, reason := contextOperationResult(err)
				return "", reason, nil, false, outcome
			}
		}
		for raw := range strings.SplitSeq(value, ",") {
			if order&15 == 0 {
				if err := ctx.Err(); err != nil {
					outcome, reason := contextOperationResult(err)
					return "", reason, nil, false, outcome
				}
			}
			rangeValue, quality, ok := parseLanguageRange(raw)
			if !ok || len(rangeValue) > r.limits.MaxTagBytes {
				malformed = true
				order++
				continue
			}
			if rangeValue == "*" {
				if quality == 0 {
					excludeWildcard = true
				} else {
					ranges = append(ranges, weightedRange{wildcard: true, quality: quality, order: order})
				}
				order++
				continue
			}
			tag, _, err := canonicalLocale(rangeValue, r.limits.MaxTagBytes)
			if err != nil {
				malformed = true
				order++
				continue
			}
			if quality == 0 {
				excluded = append(excluded, tag)
			} else {
				ranges = append(ranges, weightedRange{tag: tag, quality: quality, order: order})
			}
			order++
		}
	}
	if err := ctx.Err(); err != nil {
		outcome, reason := contextOperationResult(err)
		return "", reason, nil, false, outcome
	}
	if len(ranges) == 0 {
		if len(excluded) > 0 || excludeWildcard {
			return "", ReasonExcluded, excluded, excludeWildcard, OutcomeNoMatch
		}
		if malformed {
			return "", ReasonMalformed, nil, false, OutcomeInvalid
		}
		return "", ReasonUnsupported, nil, false, OutcomeNoMatch
	}

	sort.SliceStable(ranges, func(i, j int) bool {
		return ranges[i].quality > ranges[j].quality
	})
	for candidateIndex, candidate := range ranges {
		if candidateIndex&7 == 0 {
			if err := ctx.Err(); err != nil {
				outcome, reason := contextOperationResult(err)
				return "", reason, nil, false, outcome
			}
		}
		if candidate.quality == 0 {
			continue
		}
		if candidate.wildcard {
			if !priorWildcard && !excludeWildcard {
				allExclusions := make([]language.Tag, 0, len(priorExclusions)+len(excluded))
				allExclusions = append(allExclusions, priorExclusions...)
				allExclusions = append(allExclusions, excluded...)
				locale, wildcardErr := r.wildcard(ctx, allExclusions)
				if wildcardErr != nil {
					outcome, reason := contextOperationResult(wildcardErr)
					return "", reason, nil, false, outcome
				}
				if locale != "" {
					return locale, ReasonWildcard, excluded, false, OutcomeSuccess
				}
			}
			continue
		}
		var allowed func(int) (bool, error)
		if priorWildcard || len(priorExclusions) != 0 || len(excluded) != 0 {
			allowed = func(index int) (bool, error) {
				if priorWildcard {
					return false, nil
				}
				priorExcluded, exclusionErr := matchesAnyContext(ctx, r.tags[index], priorExclusions)
				if exclusionErr != nil || priorExcluded {
					return false, exclusionErr
				}
				currentExcluded, exclusionErr := excludedForPositiveContext(ctx, r.tags[index], candidate.tag, excluded)
				return !currentExcluded, exclusionErr
			}
		}
		locale, reason, ok, matchErr := r.match(ctx, candidate.tag, allowed)
		if matchErr != nil {
			outcome, contextReason := contextOperationResult(matchErr)
			return "", contextReason, nil, false, outcome
		}
		if ok {
			return locale, reason, excluded, excludeWildcard, outcomeForReason(reason)
		}
	}
	if malformed {
		return "", ReasonMalformed, excluded, excludeWildcard, OutcomeInvalid
	}
	if priorWildcard || len(priorExclusions) > 0 || excludeWildcard || len(excluded) > 0 {
		return "", ReasonExcluded, excluded, excludeWildcard, OutcomeNoMatch
	}
	return "", ReasonUnsupported, excluded, false, OutcomeNoMatch
}

func (r *Resolver) match(ctx context.Context, tag language.Tag, allowed func(int) (bool, error)) (string, Reason, bool, error) {
	eligible := func(index int) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if allowed == nil {
			return true, nil
		}
		return allowed(index)
	}
	canonical := tag.String()
	if index, ok := r.index[canonical]; ok {
		allowed, err := eligible(index)
		if err != nil {
			return "", ReasonContextCanceled, false, err
		}
		if allowed {
			return canonical, ReasonExact, true, nil
		}
	}
	for depth, current := 0, canonical; depth < r.limits.MaxFallbackDepth; depth++ {
		if err := ctx.Err(); err != nil {
			return "", ReasonContextCanceled, false, err
		}
		parent, ok := lookupLocaleParent(current, r.parents)
		if !ok {
			break
		}
		if index, ok := r.index[parent]; ok {
			allowed, err := eligible(index)
			if err != nil {
				return "", ReasonContextCanceled, false, err
			}
			if allowed {
				return parent, ReasonLookup, true, nil
			}
		}
		current = parent
	}
	if r.mode == MatchBestFit {
		tags := r.tags
		locales := r.supported
		matcher := r.matcher
		if allowed != nil {
			tags = make([]language.Tag, 0, len(r.tags))
			locales = make([]string, 0, len(r.supported))
			for index := range r.tags {
				if index&15 == 0 {
					if err := ctx.Err(); err != nil {
						return "", ReasonContextCanceled, false, err
					}
				}
				eligible, err := eligible(index)
				if err != nil {
					return "", ReasonContextCanceled, false, err
				}
				if eligible {
					tags = append(tags, r.tags[index])
					locales = append(locales, r.supported[index])
				}
			}
			if len(tags) == 0 {
				return "", ReasonUnsupported, false, nil
			}
			if err := ctx.Err(); err != nil {
				return "", ReasonContextCanceled, false, err
			}
			matcher = language.NewMatcher(tags)
		}
		_, index, confidence := matcher.Match(tag)
		if err := ctx.Err(); err != nil {
			return "", ReasonContextCanceled, false, err
		}
		if confidence != language.No {
			return locales[index], ReasonBestFit, true, nil
		}
	}
	return "", ReasonUnsupported, false, ctx.Err()
}

func lookupLocaleParent(locale string, parents map[string]string) (string, bool) {
	if parent, ok := parents[locale]; ok {
		return parent, true
	}
	cut := strings.LastIndexByte(locale, '-')
	if cut < 0 {
		return "", false
	}
	if cut >= 2 && locale[cut-2] == '-' {
		cut -= 2
	}
	if cut <= 0 {
		return "", false
	}
	return locale[:cut], true
}

func (r *Resolver) wildcard(ctx context.Context, excluded []language.Tag) (string, error) {
	if index, ok := r.index[r.defaultLocale]; ok {
		excluded, err := matchesAnyContext(ctx, r.tags[index], excluded)
		if err != nil {
			return "", err
		}
		if !excluded {
			return r.defaultLocale, nil
		}
	}
	for i, locale := range r.supported {
		if i&15 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		isExcluded, err := matchesAnyContext(ctx, r.tags[i], excluded)
		if err != nil {
			return "", err
		}
		if !isExcluded {
			return locale, nil
		}
	}
	return "", ctx.Err()
}

func parseLanguageRange(raw string) (string, int, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ";")
	if len(parts) == 0 || len(parts) > 2 {
		return "", 0, false
	}
	rangeValue := strings.TrimSpace(parts[0])
	if rangeValue == "" {
		return "", 0, false
	}
	quality := 1000
	if len(parts) == 2 {
		parameter := strings.TrimSpace(parts[1])
		name, value, ok := strings.Cut(parameter, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
			return "", 0, false
		}
		var valid bool
		quality, valid = parseQuality(strings.TrimSpace(value))
		if !valid {
			return "", 0, false
		}
	}
	return rangeValue, quality, true
}

func parseQuality(value string) (int, bool) {
	if value == "0" || value == "0." {
		return 0, true
	}
	if value == "1" || value == "1." {
		return 1000, true
	}
	whole, fraction, found := strings.Cut(value, ".")
	if !found || (whole != "0" && whole != "1") || len(fraction) > 3 {
		return 0, false
	}
	if whole == "1" && strings.Trim(fraction, "0") != "" {
		return 0, false
	}
	if whole == "1" {
		return 1000, true
	}
	if fraction == "" {
		if whole == "" {
			return 0, false
		}
		return 1000 * int(whole[0]-'0'), true
	}
	for _, r := range fraction {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	fraction += strings.Repeat("0", 3-len(fraction))
	q, err := strconv.Atoi(fraction)
	return q, err == nil
}

func outcomeForReason(reason Reason) Outcome {
	switch reason {
	case ReasonExact, ReasonWildcard:
		return OutcomeSuccess
	case ReasonLookup, ReasonBestFit:
		return OutcomeFallback
	case ReasonPolicyDefault:
		return OutcomeDefault
	case ReasonLimit:
		return OutcomeLimited
	case ReasonMalformed:
		return OutcomeInvalid
	default:
		return OutcomeNoMatch
	}
}

func validChoiceSource(source ChoiceSource) bool {
	return source >= SourceExplicit && source <= SourceApplication
}

func matchesAnyContext(ctx context.Context, tag language.Tag, ranges []language.Tag) (bool, error) {
	tagName := tag.String()
	for index, candidate := range ranges {
		if index&15 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		candidateName := candidate.String()
		if tagName == candidateName || strings.HasPrefix(tagName, candidateName+"-") {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func excludedForPositiveContext(ctx context.Context, locale, positive language.Tag, excluded []language.Tag) (bool, error) {
	localeName := locale.String()
	positiveName := positive.String()
	for index, exclusion := range excluded {
		if index&15 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		exclusionName := exclusion.String()
		if localeName != exclusionName && !strings.HasPrefix(localeName, exclusionName+"-") {
			continue
		}
		if strings.HasPrefix(positiveName, exclusionName+"-") &&
			strings.HasPrefix(localeName, exclusionName+"-") &&
			(localeName == positiveName || strings.HasPrefix(localeName, positiveName+"-") || strings.HasPrefix(positiveName, localeName+"-")) {
			continue
		}
		return true, nil
	}
	return false, ctx.Err()
}

func canonicalLocale(raw string, maxBytes int) (language.Tag, string, error) {
	if len(raw) > maxBytes {
		return language.Und, "", fmt.Errorf("locale exceeds %d bytes", maxBytes)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return language.Und, "", fmt.Errorf("locale is empty")
	}
	tag, err := language.Parse(raw)
	if err != nil {
		return language.Und, "", err
	}
	tag, err = language.All.Canonicalize(tag)
	if err != nil || tag == language.Und {
		return language.Und, "", fmt.Errorf("locale %q has no language", raw)
	}
	return tag, tag.String(), nil
}

func checkedLocaleLimits(limits LocaleLimits) (LocaleLimits, error) {
	if limits.MaxChoices == 0 {
		limits.MaxChoices = 16
	}
	if limits.MaxHeaderBytes == 0 {
		limits.MaxHeaderBytes = 8192
	}
	if limits.MaxRanges == 0 {
		limits.MaxRanges = 32
	}
	if limits.MaxSupported == 0 {
		limits.MaxSupported = 128
	}
	if limits.MaxTagBytes == 0 {
		limits.MaxTagBytes = 255
	}
	if limits.MaxFallbackDepth == 0 {
		limits.MaxFallbackDepth = 16
	}
	if limits.MaxChoices < 1 || limits.MaxChoices > 64 || limits.MaxHeaderBytes < 1 || limits.MaxHeaderBytes > maxAcceptLanguageBytes || limits.MaxRanges < 1 || limits.MaxRanges > maxAcceptLanguageRanges || limits.MaxSupported < 1 || limits.MaxSupported > 1024 || limits.MaxTagBytes < 1 || limits.MaxTagBytes > 512 || limits.MaxFallbackDepth < 1 || limits.MaxFallbackDepth > 64 {
		return LocaleLimits{}, fmt.Errorf("%w: locale limits are outside their supported bounds", ErrInvalidLocale)
	}
	return limits, nil
}

func effectiveLocaleParent(locale string, parents map[string]string) (string, bool) {
	if parent, ok := parents[locale]; ok {
		return parent, true
	}
	tag, err := language.Parse(locale)
	if err != nil {
		return "", false
	}
	parent := tag.Parent()
	if parent == language.Und || parent.String() == locale {
		return "", false
	}
	return parent.String(), true
}

func effectiveLocaleGraphProblem(seeds []string, parents map[string]string, maxDepth int) (string, string) {
	return localeGraphProblem(seeds, parents, maxDepth, effectiveLocaleParent)
}

func lookupLocaleGraphProblem(seeds []string, parents map[string]string, maxDepth int) (string, string) {
	return localeGraphProblem(seeds, parents, maxDepth, lookupLocaleParent)
}

func localeGraphProblem(seeds []string, parents map[string]string, maxDepth int, parentOf func(string, map[string]string) (string, bool)) (string, string) {
	slices.Sort(seeds)
	seeds = slices.Compact(seeds)
	for _, start := range seeds {
		seen := map[string]bool{start: true}
		current := start
		for depth := 0; ; depth++ {
			next, ok := parentOf(current, parents)
			if !ok {
				break
			}
			if seen[next] {
				return next, ""
			}
			if depth >= maxDepth {
				return "", start
			}
			seen[next] = true
			current = next
		}
	}
	return "", ""
}
