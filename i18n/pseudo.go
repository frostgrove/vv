package i18n

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/kaptinlin/messageformat-go/pkg/datamodel"
)

type PseudoMode uint8

const (
	PseudoAccent PseudoMode = iota + 1
	PseudoRTL
)

func (m PseudoMode) String() string {
	switch m {
	case PseudoAccent:
		return "accent"
	case PseudoRTL:
		return "rtl"
	default:
		return "unknown"
	}
}

func (m PseudoMode) Valid() bool {
	return m == PseudoAccent || m == PseudoRTL
}

type PseudoSpec struct {
	Locale         string
	Mode           PseudoMode
	MaxOutputBytes int
}

func Pseudo(spec CatalogSpec, pseudo PseudoSpec) (result CatalogSpec, err error) {
	return PseudoContext(context.Background(), spec, pseudo)
}

func PseudoContext(ctx context.Context, spec CatalogSpec, pseudo PseudoSpec) (result CatalogSpec, err error) {
	defer func() {
		if recover() != nil {
			result = CatalogSpec{}
			err = fmt.Errorf("%w: message engine rejected the catalog", ErrInvalidCatalog)
		}
	}()
	if ctx == nil {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	if !pseudo.Mode.Valid() {
		return CatalogSpec{}, fmt.Errorf("%w: unsupported pseudolocale mode %q", ErrInvalidCatalog, pseudo.Mode.String())
	}
	limits, err := checkedLimits(spec.Limits)
	if err != nil {
		return CatalogSpec{}, err
	}
	maximum := pseudo.MaxOutputBytes
	if maximum == 0 || maximum > limits.MaxCatalogBytes {
		maximum = limits.MaxCatalogBytes
	}
	if maximum < 1 {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale output limit must be positive", ErrLimitExceeded)
	}
	artifactLimits := ArtifactLimits{MaxBytes: maximum, MaxDepth: maximumArtifactDepth, MaxMembers: maximumArtifactMembers}
	var outputFloor artifactFloorCounter
	if err := checkSourceArtifactFloorIntoContext(ctx, spec, limits, artifactLimits, &outputFloor); err != nil {
		return CatalogSpec{}, err
	}
	_, target, err := canonicalLocale(pseudo.Locale, limits.Locale.MaxTagBytes)
	if err != nil {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale: %v", ErrInvalidLocale, err)
	}
	_, sourceLocale, sourceLocaleErr := canonicalLocale(spec.SourceLocale, limits.Locale.MaxTagBytes)
	if sourceLocaleErr == nil && target == sourceLocale {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale %q is the source locale", ErrInvalidCatalog, target)
	}
	targetSupported := false
	for _, locale := range spec.Supported {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		_, canonical, localeErr := canonicalLocale(locale, limits.Locale.MaxTagBytes)
		if localeErr == nil && canonical == target {
			targetSupported = true
		}
	}
	if !targetSupported {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale %q is not supported", ErrInvalidLocale, target)
	}
	material, translations, err := pseudoInputMaterialContext(ctx, spec, limits)
	if err != nil {
		return CatalogSpec{}, err
	}
	if translations > limits.MaxTranslations {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale exceeds %d translations", ErrLimitExceeded, limits.MaxTranslations)
	}
	messageCount := 0
	for _, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		if messageCount > limits.MaxTranslations-translations || len(module.Messages) > limits.MaxTranslations-translations-messageCount {
			return CatalogSpec{}, fmt.Errorf("%w: pseudolocale exceeds %d translations", ErrLimitExceeded, limits.MaxTranslations)
		}
		messageCount += len(module.Messages)
	}
	preflightOutput := outputFloor
	preflightMaterial := material
	placeholderDigest := strings.Repeat("0", 64)
	for moduleIndex := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		module := &spec.Modules[moduleIndex]
		for messageIndex := range module.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			message := &module.Messages[messageIndex]
			key := message.Key
			if key == "" {
				key = Qualify(module.Name, message.ID)
			}
			translationCollision, collisionErr := pseudoTranslationCollidesContext(ctx, *message, target, limits.Locale.MaxTagBytes)
			if collisionErr != nil {
				return CatalogSpec{}, collisionErr
			}
			overrideCollision, collisionErr := pseudoOverrideCollidesContext(ctx, spec.Overrides, key, target, limits.Locale.MaxTagBytes)
			if collisionErr != nil {
				return CatalogSpec{}, collisionErr
			}
			if translationCollision || overrideCollision {
				return CatalogSpec{}, fmt.Errorf("%w: pseudolocale %q already defines %q", ErrInvalidCatalog, target, key)
			}
			lowerBound := sourceTranslation{
				Locale: target, Text: message.Source, Review: ReviewRequired.String(),
				ContractRevision: message.Revision, SourceDigest: placeholderDigest, ReviewDigest: placeholderDigest,
			}
			if !preflightOutput.addArrayValue(lowerBound, len(message.Translations), 7) {
				if preflightOutput.err != nil {
					return CatalogSpec{}, preflightOutput.err
				}
				return CatalogSpec{}, fmt.Errorf("%w: pseudolocale output exceeds %d bytes", ErrLimitExceeded, maximum)
			}
			if !preflightMaterial.add(target, message.Source, message.Revision, placeholderDigest, placeholderDigest) {
				return CatalogSpec{}, fmt.Errorf("%w: pseudolocale exceeds catalog material bounds", ErrLimitExceeded)
			}
		}
	}
	type pendingTranslation struct {
		moduleIndex  int
		messageIndex int
		translation  Translation
	}
	pending := make([]pendingTranslation, 0, min(messageCount, 1024))
	for moduleIndex := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		module := &spec.Modules[moduleIndex]
		for messageIndex := range module.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			message := &module.Messages[messageIndex]
			key := message.Key
			if key == "" {
				key = Qualify(module.Name, message.ID)
			}
			text, transformErr := pseudolocalizeMessageContext(ctx, message.Source, pseudo.Mode, limits.MaxTemplateBytes)
			if transformErr != nil {
				if errors.Is(transformErr, ErrLimitExceeded) {
					return CatalogSpec{}, transformErr
				}
				return CatalogSpec{}, fmt.Errorf("%w: pseudolocale %q for %q: %v", ErrInvalidCatalog, target, key, transformErr)
			}
			exact := sourceTranslation{
				Locale: target, Text: text, Review: ReviewRequired.String(),
				ContractRevision: message.Revision, SourceDigest: placeholderDigest, ReviewDigest: placeholderDigest,
			}
			if !outputFloor.addArrayValue(exact, len(message.Translations), 7) {
				if outputFloor.err != nil {
					return CatalogSpec{}, outputFloor.err
				}
				return CatalogSpec{}, fmt.Errorf("%w: pseudolocale output exceeds %d bytes", ErrLimitExceeded, maximum)
			}
			if !material.add(target, text, message.Revision, placeholderDigest, placeholderDigest) {
				return CatalogSpec{}, fmt.Errorf("%w: pseudolocale exceeds catalog material bounds", ErrLimitExceeded)
			}
			pending = append(pending, pendingTranslation{
				moduleIndex: moduleIndex, messageIndex: messageIndex,
				translation: Translation{
					Locale: target, Text: text, Review: ReviewRequired,
					ContractRevision: message.Revision,
				},
			})
		}
	}
	inputValidation, err := pseudoValidationCatalogContext(ctx, spec)
	if err != nil {
		return CatalogSpec{}, err
	}
	inputValidation.Required = slices.DeleteFunc(inputValidation.Required, func(locale string) bool {
		_, canonical, localeErr := canonicalLocale(locale, limits.Locale.MaxTagBytes)
		return localeErr == nil && canonical == target
	})
	snapshot, err := NewContext(ctx, inputValidation)
	if err != nil {
		return CatalogSpec{}, err
	}
	if target == snapshot.sourceLocale {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale %q is the source locale", ErrInvalidCatalog, target)
	}
	if !slices.Contains(snapshot.supported, target) {
		return CatalogSpec{}, fmt.Errorf("%w: pseudolocale %q is not supported", ErrInvalidLocale, target)
	}

	for pendingIndex := range pending {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		value := &pending[pendingIndex]
		module := &spec.Modules[value.moduleIndex]
		message := &module.Messages[value.messageIndex]
		key := message.Key
		if key == "" {
			key = Qualify(module.Name, message.ID)
		}
		digest, digestErr := ExpectedSourceDigestForLocale(snapshot.Profile(), spec.SourceLocale, module.Name, *message)
		if digestErr != nil {
			return CatalogSpec{}, fmt.Errorf("%w: source identity for %q: %v", ErrInvalidCatalog, key, digestErr)
		}
		reviewDigest, reviewDigestErr := ExpectedReviewDigest(digest, target, value.translation.Text)
		if reviewDigestErr != nil {
			return CatalogSpec{}, fmt.Errorf("%w: review identity for %q: %v", ErrInvalidCatalog, key, reviewDigestErr)
		}
		value.translation.SourceDigest = digest
		value.translation.ReviewDigest = reviewDigest
	}

	result, err = cloneCatalogSpecForPseudoContext(ctx, spec)
	if err != nil {
		return CatalogSpec{}, err
	}
	for _, value := range pending {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		message := &result.Modules[value.moduleIndex].Messages[value.messageIndex]
		message.Translations = append(message.Translations, value.translation)
	}

	validation, err := pseudoValidationCatalogContext(ctx, result)
	if err != nil {
		return CatalogSpec{}, err
	}
	if _, err := NewContext(ctx, validation); err != nil {
		return CatalogSpec{}, err
	}
	return result, nil
}

func pseudoInputMaterial(spec CatalogSpec, limits Limits) (artifactCounter, int, error) {
	return pseudoInputMaterialContext(context.Background(), spec, limits)
}

func pseudoInputMaterialContext(ctx context.Context, spec CatalogSpec, limits Limits) (artifactCounter, int, error) {
	counter := artifactCounter{maxBytes: limits.MaxCatalogBytes, maxItems: limits.MaxCatalogItems}
	add := func(values ...string) error {
		if !counter.add(values...) {
			return fmt.Errorf("%w: catalog material exceeds configured bounds", ErrLimitExceeded)
		}
		return nil
	}
	if err := add(spec.Revision, spec.Profile, spec.SourceLocale, spec.DefaultLocale, spec.DefaultTimeZone, spec.TimeZoneDataVersion); err != nil {
		return counter, 0, err
	}
	for _, value := range spec.Supported {
		if err := ctx.Err(); err != nil {
			return counter, 0, err
		}
		if err := add(value); err != nil {
			return counter, 0, err
		}
	}
	for _, value := range spec.Required {
		if err := ctx.Err(); err != nil {
			return counter, 0, err
		}
		if err := add(value); err != nil {
			return counter, 0, err
		}
	}
	for _, edge := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return counter, 0, err
		}
		if err := add(edge.Locale, edge.Parent); err != nil {
			return counter, 0, err
		}
	}
	translations := len(spec.Overrides)
	for _, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return counter, 0, err
		}
		if err := add(module.Name); err != nil {
			return counter, 0, err
		}
		for _, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return counter, 0, err
			}
			translations += 1 + len(message.Translations)
			if err := add(message.ID, string(message.Key), message.Revision, message.Source, message.Description); err != nil {
				return counter, 0, err
			}
			for _, argument := range message.Arguments {
				if err := ctx.Err(); err != nil {
					return counter, 0, err
				}
				if err := add(argument.Name); err != nil {
					return counter, 0, err
				}
				for _, value := range argument.Values {
					if err := ctx.Err(); err != nil {
						return counter, 0, err
					}
					if err := add(value); err != nil {
						return counter, 0, err
					}
				}
			}
			for _, markup := range message.Markup {
				if err := ctx.Err(); err != nil {
					return counter, 0, err
				}
				if err := add(markup); err != nil {
					return counter, 0, err
				}
			}
			for _, translation := range message.Translations {
				if err := ctx.Err(); err != nil {
					return counter, 0, err
				}
				if err := add(translation.Locale, translation.Text, translation.ContractRevision, translation.SourceDigest, translation.ReviewDigest); err != nil {
					return counter, 0, err
				}
			}
		}
	}
	for _, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return counter, 0, err
		}
		if err := add(string(override.Key), override.Locale, override.Text, override.ContractRevision, override.SourceDigest, override.ReviewDigest); err != nil {
			return counter, 0, err
		}
	}
	return counter, translations, nil
}

func pseudoValidationCatalog(spec CatalogSpec) CatalogSpec {
	validation, _ := pseudoValidationCatalogContext(context.Background(), spec)
	return validation
}

func pseudoValidationCatalogContext(ctx context.Context, spec CatalogSpec) (CatalogSpec, error) {
	validation, err := cloneCatalogSpecForPseudoContext(ctx, spec)
	if err != nil {
		return CatalogSpec{}, err
	}
	for moduleIndex := range validation.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		for messageIndex := range validation.Modules[moduleIndex].Messages {
			message := &validation.Modules[moduleIndex].Messages[messageIndex]
			digest, _ := ExpectedSourceDigestForLocale(validation.Profile, validation.SourceLocale, validation.Modules[moduleIndex].Name, *message)
			translations := message.Translations
			for translationIndex := range translations {
				if translations[translationIndex].Review == ReviewRequired {
					translations[translationIndex].Review = ReviewApproved
					translations[translationIndex].ContractRevision = message.Revision
					translations[translationIndex].SourceDigest = digest
					translations[translationIndex].ReviewDigest, _ = ExpectedReviewDigest(digest, translations[translationIndex].Locale, translations[translationIndex].Text)
				}
			}
		}
	}
	return validation, ctx.Err()
}

func cloneCatalogSpecForPseudo(spec CatalogSpec) CatalogSpec {
	clone, _ := cloneCatalogSpecForPseudoContext(context.Background(), spec)
	return clone
}

func cloneCatalogSpecForPseudoContext(ctx context.Context, spec CatalogSpec) (CatalogSpec, error) {
	return cloneCatalogSpecContext(ctx, spec)
}

func pseudoTranslationCollides(message MessageSpec, target string, maxTagBytes int) bool {
	collides, _ := pseudoTranslationCollidesContext(context.Background(), message, target, maxTagBytes)
	return collides
}

func pseudoTranslationCollidesContext(ctx context.Context, message MessageSpec, target string, maxTagBytes int) (bool, error) {
	for _, translation := range message.Translations {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		_, locale, err := canonicalLocale(translation.Locale, maxTagBytes)
		if err == nil && locale == target {
			return true, nil
		}
	}
	return false, nil
}

func pseudoOverrideCollides(overrides []Override, key Key, target string, maxTagBytes int) bool {
	collides, _ := pseudoOverrideCollidesContext(context.Background(), overrides, key, target, maxTagBytes)
	return collides
}

func pseudoOverrideCollidesContext(ctx context.Context, overrides []Override, key Key, target string, maxTagBytes int) (bool, error) {
	for _, override := range overrides {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if override.Key != key {
			continue
		}
		_, locale, err := canonicalLocale(override.Locale, maxTagBytes)
		if err == nil && locale == target {
			return true, nil
		}
	}
	return false, nil
}

func pseudolocalizeMessage(source string, mode PseudoMode, maximum int) (string, error) {
	return pseudolocalizeMessageContext(context.Background(), source, mode, maximum)
}

func pseudolocalizeMessageContext(ctx context.Context, source string, mode PseudoMode, maximum int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	message, err := datamodel.ParseMessage(source)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	transformed, err := transformPseudoMessageContext(ctx, message, mode)
	if err != nil {
		return "", err
	}
	text, err := datamodel.StringifyMessage(transformed)
	if err != nil {
		return "", err
	}
	if len(text) > maximum {
		return "", fmt.Errorf("%w: pseudolocalized template exceeds %d bytes", ErrLimitExceeded, maximum)
	}
	return text, ctx.Err()
}

func transformPseudoMessage(message datamodel.Message, mode PseudoMode) (datamodel.Message, error) {
	return transformPseudoMessageContext(context.Background(), message, mode)
}

func transformPseudoMessageContext(ctx context.Context, message datamodel.Message, mode PseudoMode) (datamodel.Message, error) {
	declarations, err := transformPseudoDeclarationsContext(ctx, message.Declarations(), mode)
	if err != nil {
		return nil, err
	}
	switch message := message.(type) {
	case *datamodel.PatternMessage:
		pattern, err := transformPseudoPatternContext(ctx, message.Pattern(), mode)
		if err != nil {
			return nil, err
		}
		return datamodel.NewPatternMessage(declarations, pattern, message.Comment())
	case *datamodel.SelectMessage:
		variants := message.Variants()
		for index := range variants {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			pattern, err := transformPseudoPatternContext(ctx, variants[index].Value(), mode)
			if err != nil {
				return nil, err
			}
			variant, err := datamodel.NewVariant(variants[index].Keys(), pattern)
			if err != nil {
				return nil, err
			}
			variants[index] = *variant
		}
		return datamodel.NewSelectMessage(declarations, message.Selectors(), variants, message.Comment())
	default:
		return nil, fmt.Errorf("unsupported MF2 message model %T", message)
	}
}

func transformPseudoDeclarations(declarations []datamodel.Declaration, mode PseudoMode) ([]datamodel.Declaration, error) {
	return transformPseudoDeclarationsContext(context.Background(), declarations, mode)
}

func transformPseudoDeclarationsContext(ctx context.Context, declarations []datamodel.Declaration, mode PseudoMode) ([]datamodel.Declaration, error) {
	transformed := slices.Clone(declarations)
	for index, declaration := range transformed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		local, ok := declaration.(*datamodel.LocalDeclaration)
		if !ok {
			continue
		}
		expression := local.Value()
		literal, ok := expression.Arg().(*datamodel.Literal)
		if !ok || expression.FunctionRef() != nil {
			continue
		}
		text, err := pseudoTextContext(ctx, literal.Value(), mode)
		if err != nil {
			return nil, err
		}
		value, err := datamodel.NewExpression(datamodel.NewLiteral(text), nil, expression.Attributes())
		if err != nil {
			return nil, err
		}
		transformed[index], err = datamodel.NewLocalDeclaration(local.Name(), value)
		if err != nil {
			return nil, err
		}
	}
	return transformed, nil
}

func transformPseudoPattern(pattern datamodel.Pattern, mode PseudoMode) (datamodel.Pattern, error) {
	return transformPseudoPatternContext(context.Background(), pattern, mode)
}

func transformPseudoPatternContext(ctx context.Context, pattern datamodel.Pattern, mode PseudoMode) (datamodel.Pattern, error) {
	elements := pattern.Elements()
	for index, element := range elements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch element := element.(type) {
		case *datamodel.TextElement:
			text, err := pseudoTextContext(ctx, element.Value(), mode)
			if err != nil {
				return nil, err
			}
			elements[index] = datamodel.NewTextElement(text)
		case *datamodel.Expression:
			literal, ok := element.Arg().(*datamodel.Literal)
			if !ok || element.FunctionRef() != nil {
				continue
			}
			text, err := pseudoTextContext(ctx, literal.Value(), mode)
			if err != nil {
				return nil, err
			}
			expression, err := datamodel.NewExpression(datamodel.NewLiteral(text), nil, element.Attributes())
			if err != nil {
				return nil, err
			}
			elements[index] = expression
		}
	}
	return datamodel.NewPattern(elements)
}

func pseudoText(value string, mode PseudoMode) string {
	text, _ := pseudoTextContext(context.Background(), value, mode)
	return text
}

func pseudoTextContext(ctx context.Context, value string, mode PseudoMode) (string, error) {
	if mode == PseudoRTL {
		return pseudoRTLTextContext(ctx, value)
	}
	return pseudoAccentTextContext(ctx, value)
}

func pseudoAccentText(value string) string {
	text, _ := pseudoAccentTextContext(context.Background(), value)
	return text
}

func pseudoAccentTextContext(ctx context.Context, value string) (string, error) {
	if value == "" {
		return "", ctx.Err()
	}
	var out strings.Builder
	out.Grow(len(value) + len(value)/3 + 6)
	out.WriteRune('⟦')
	letters := 0
	for _, r := range value {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		mapped, letter := pseudoAccentRune(r)
		out.WriteRune(mapped)
		if letter {
			letters++
			if letters%3 == 0 {
				out.WriteRune('~')
			}
		}
	}
	out.WriteRune('⟧')
	return out.String(), ctx.Err()
}

func pseudoRTLText(value string) string {
	text, _ := pseudoRTLTextContext(context.Background(), value)
	return text
}

func pseudoRTLTextContext(ctx context.Context, value string) (string, error) {
	if value == "" {
		return "", ctx.Err()
	}
	expanded := make([]rune, 0, len(value)+len(value)/3)
	letters := 0
	for _, r := range value {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		mapped, letter := pseudoRTLRune(r)
		expanded = append(expanded, mapped)
		if letter {
			letters++
			if letters%3 == 0 {
				expanded = append(expanded, '־')
			}
		}
	}
	reversed := make([]rune, 0, len(expanded)+2)
	reversed = append(reversed, '‏')
	segmentStart := 0
	for index, r := range expanded {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !isPseudoDirectionalControl(r) {
			continue
		}
		reversed = appendReversedPseudoRun(reversed, expanded[segmentStart:index])
		reversed = append(reversed, r)
		segmentStart = index + 1
	}
	reversed = appendReversedPseudoRun(reversed, expanded[segmentStart:])
	reversed = append(reversed, '‏')
	return string(reversed), ctx.Err()
}

func appendReversedPseudoRun(target, value []rune) []rune {
	for index := len(value) - 1; index >= 0; index-- {
		target = append(target, mirrorPseudoRune(value[index]))
	}
	return target
}

func isPseudoDirectionalControl(r rune) bool {
	return r == '؜' || r == '‎' || r == '‏' || r >= '⁦' && r <= '⁩'
}

func pseudoAccentRune(r rune) (rune, bool) {
	const source = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	index := strings.IndexRune(source, r)
	if index < 0 {
		return r, unicode.IsLetter(r)
	}
	return pseudoAccentRunes[index], true
}

func pseudoRTLRune(r rune) (rune, bool) {
	const source = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	index := strings.IndexRune(source, r)
	if index < 0 {
		return r, unicode.IsLetter(r)
	}
	return pseudoRTLRunes[index], true
}

var pseudoAccentRunes = []rune("ÅƁĆĎĖƑĜĤĪĴĶĹṀŃÖƤɊŔŠŦŮṼŴẊÝŽåɓćďėƒĝĥīĵķĺṁńöƥɋŕšŧůṽŵẋýž")

var pseudoRTLRunes = []rune("אבגדהוזחטיכלמנסעפצקרשתוכיזאבגדהוזחטיכלמנסעפצקרשתוכיז")

func mirrorPseudoRune(r rune) rune {
	switch r {
	case '(':
		return ')'
	case ')':
		return '('
	case '[':
		return ']'
	case ']':
		return '['
	case '{':
		return '}'
	case '}':
		return '{'
	case '<':
		return '>'
	case '>':
		return '<'
	default:
		return r
	}
}
