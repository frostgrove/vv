package main

import (
	"context"
	"fmt"
	"slices"
	"unicode/utf8"

	"golang.org/x/text/language"

	"github.com/frostgrove/vv/i18n"
)

type reviewSelector struct {
	locale string
	key    i18n.Key
	scope  string
	state  i18n.ReviewState
}

func reviewCatalog(spec i18n.CatalogSpec, selector reviewSelector) (i18n.CatalogSpec, int, error) {
	return reviewCatalogContext(context.Background(), spec, selector)
}

func reviewCatalogContext(ctx context.Context, spec i18n.CatalogSpec, selector reviewSelector) (i18n.CatalogSpec, int, error) {
	return reviewCatalogWithClone(ctx, spec, selector, cloneReviewCatalogContext)
}

func reviewCatalogWithClone(ctx context.Context, spec i18n.CatalogSpec, selector reviewSelector, clone func(context.Context, i18n.CatalogSpec) (i18n.CatalogSpec, error)) (i18n.CatalogSpec, int, error) {
	limits := i18n.DefaultArtifactLimits()
	return reviewCatalogBoundedWithClone(ctx, spec, selector, i18n.SourceCodec{Limits: limits, CatalogLimits: spec.Limits}, limits.MaxBytes, clone)
}

func reviewCatalogBoundedContext(ctx context.Context, spec i18n.CatalogSpec, selector reviewSelector, codec i18n.SourceCodec, maximum int) (i18n.CatalogSpec, int, error) {
	return reviewCatalogBoundedWithClone(ctx, spec, selector, codec, maximum, cloneReviewCatalogContext)
}

func reviewCatalogBoundedWithClone(ctx context.Context, spec i18n.CatalogSpec, selector reviewSelector, codec i18n.SourceCodec, maximum int, clone func(context.Context, i18n.CatalogSpec) (i18n.CatalogSpec, error)) (i18n.CatalogSpec, int, error) {
	if ctx == nil {
		return i18n.CatalogSpec{}, 0, fmt.Errorf("review context is nil")
	}
	if err := ctx.Err(); err != nil {
		return i18n.CatalogSpec{}, 0, err
	}
	locale, err := canonicalCommandLocale(selector.locale)
	if err != nil {
		return i18n.CatalogSpec{}, 0, err
	}
	if selector.scope != "all" && selector.scope != "translations" && selector.scope != "overrides" {
		return i18n.CatalogSpec{}, 0, fmt.Errorf("unknown review scope %q", selector.scope)
	}
	if selector.state != i18n.ReviewApproved && selector.state != i18n.ReviewRequired && selector.state != i18n.ReviewRejected {
		return i18n.CatalogSpec{}, 0, fmt.Errorf("unsupported review state %q", selector.state.String())
	}
	if maximum < 1 {
		return i18n.CatalogSpec{}, 0, fmt.Errorf("%w: reviewed source output limit must be positive", i18n.ErrLimitExceeded)
	}
	baseline, err := codec.EncodedSizeContext(ctx, spec)
	if err != nil {
		return i18n.CatalogSpec{}, 0, err
	}
	delta, selected, err := reviewStampDeltaContext(ctx, spec, selector, locale)
	if err != nil {
		return i18n.CatalogSpec{}, 0, err
	}
	if selected == 0 {
		if selector.key == "" {
			return i18n.CatalogSpec{}, 0, fmt.Errorf("no %s entries found for locale %q", selector.scope, locale)
		}
		return i18n.CatalogSpec{}, 0, fmt.Errorf("no %s entries found for key %q and locale %q", selector.scope, selector.key, locale)
	}
	if baseline > maximum || delta > maximum-baseline {
		return i18n.CatalogSpec{}, 0, fmt.Errorf("%w: reviewed source exceeds %d bytes", i18n.ErrLimitExceeded, maximum)
	}
	spec, err = clone(ctx, spec)
	if err != nil {
		return i18n.CatalogSpec{}, 0, err
	}
	digests := make(map[i18n.Key]string)
	revisions := make(map[i18n.Key]string)
	for moduleIndex := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return i18n.CatalogSpec{}, 0, err
		}
		module := &spec.Modules[moduleIndex]
		for messageIndex := range module.Messages {
			message := &module.Messages[messageIndex]
			key := message.Key
			if key == "" {
				key = i18n.Qualify(module.Name, message.ID)
			}
			if selector.key != "" && key != selector.key {
				continue
			}
			digest, digestErr := i18n.ExpectedSourceDigestForLocale(spec.Profile, spec.SourceLocale, module.Name, *message)
			if digestErr != nil {
				return i18n.CatalogSpec{}, 0, fmt.Errorf("review %q: %w", key, digestErr)
			}
			digests[key] = digest
			revisions[key] = message.Revision
		}
	}
	updated := 0
	if selector.scope != "overrides" {
		for moduleIndex := range spec.Modules {
			if err := ctx.Err(); err != nil {
				return i18n.CatalogSpec{}, 0, err
			}
			module := &spec.Modules[moduleIndex]
			for messageIndex := range module.Messages {
				message := &module.Messages[messageIndex]
				key := message.Key
				if key == "" {
					key = i18n.Qualify(module.Name, message.ID)
				}
				if selector.key != "" && key != selector.key {
					continue
				}
				for translationIndex := range message.Translations {
					translation := &message.Translations[translationIndex]
					translationLocale, localeErr := canonicalCommandLocale(translation.Locale)
					if localeErr != nil || translationLocale != locale {
						continue
					}
					translation.Review = selector.state
					translation.ContractRevision = revisions[key]
					translation.SourceDigest = digests[key]
					reviewDigest, reviewDigestErr := i18n.ExpectedReviewDigest(digests[key], translationLocale, translation.Text)
					if reviewDigestErr != nil {
						return i18n.CatalogSpec{}, 0, fmt.Errorf("review %q for %q: %w", key, translationLocale, reviewDigestErr)
					}
					translation.ReviewDigest = reviewDigest
					updated++
				}
			}
		}
	}
	if selector.scope != "translations" {
		for index := range spec.Overrides {
			if index&255 == 0 {
				if err := ctx.Err(); err != nil {
					return i18n.CatalogSpec{}, 0, err
				}
			}
			override := &spec.Overrides[index]
			if selector.key != "" && override.Key != selector.key {
				continue
			}
			overrideLocale, localeErr := canonicalCommandLocale(override.Locale)
			if localeErr != nil || overrideLocale != locale {
				continue
			}
			digest, exists := digests[override.Key]
			if !exists {
				continue
			}
			override.Review = selector.state
			override.ContractRevision = revisions[override.Key]
			override.SourceDigest = digest
			reviewDigest, reviewDigestErr := i18n.ExpectedReviewDigest(digest, overrideLocale, override.Text)
			if reviewDigestErr != nil {
				return i18n.CatalogSpec{}, 0, fmt.Errorf("review %q for %q: %w", override.Key, overrideLocale, reviewDigestErr)
			}
			override.ReviewDigest = reviewDigest
			updated++
		}
	}
	if updated == 0 {
		if selector.key == "" {
			return i18n.CatalogSpec{}, 0, fmt.Errorf("no %s entries found for locale %q", selector.scope, locale)
		}
		return i18n.CatalogSpec{}, 0, fmt.Errorf("no %s entries found for key %q and locale %q", selector.scope, selector.key, locale)
	}
	if err := ctx.Err(); err != nil {
		return i18n.CatalogSpec{}, 0, err
	}
	if selector.state == i18n.ReviewApproved {
		report := i18n.CheckContext(ctx, spec, i18n.CheckPolicy{})
		if err := ctx.Err(); err != nil {
			return i18n.CatalogSpec{}, 0, err
		}
		if finding, invalid := report.FirstStructuralError(); invalid {
			return i18n.CatalogSpec{}, 0, fmt.Errorf("cannot approve structurally invalid source: %s: %s", finding.Path, finding.Detail)
		}
	}
	return spec, updated, nil
}

func reviewStampDeltaContext(ctx context.Context, spec i18n.CatalogSpec, selector reviewSelector, locale string) (int, int, error) {
	revisions := make(map[i18n.Key]string)
	delta := 0
	selected := 0
	for _, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		for _, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			key := message.Key
			if key == "" {
				key = i18n.Qualify(module.Name, message.ID)
			}
			if selector.key != "" && key != selector.key {
				continue
			}
			revisions[key] = message.Revision
			if selector.scope == "overrides" {
				continue
			}
			for _, translation := range message.Translations {
				translationLocale, localeErr := canonicalCommandLocale(translation.Locale)
				if localeErr != nil || translationLocale != locale {
					continue
				}
				stampDelta, stampErr := reviewEntryStampDeltaContext(ctx, reviewStateWire(translation.Review), translation.ContractRevision, translation.SourceDigest, translation.ReviewDigest, selector.state.String(), message.Revision)
				if stampErr != nil {
					return 0, 0, stampErr
				}
				delta += stampDelta
				selected++
			}
		}
	}
	if selector.scope != "translations" {
		for index, override := range spec.Overrides {
			if index&255 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, 0, err
				}
			}
			if selector.key != "" && override.Key != selector.key {
				continue
			}
			overrideLocale, localeErr := canonicalCommandLocale(override.Locale)
			if localeErr != nil || overrideLocale != locale {
				continue
			}
			revision, exists := revisions[override.Key]
			if !exists {
				continue
			}
			stampDelta, stampErr := reviewEntryStampDeltaContext(ctx, reviewStateWire(override.Review), override.ContractRevision, override.SourceDigest, override.ReviewDigest, selector.state.String(), revision)
			if stampErr != nil {
				return 0, 0, stampErr
			}
			delta += stampDelta
			selected++
		}
	}
	return delta, selected, ctx.Err()
}

func reviewStateWire(state i18n.ReviewState) string {
	if !state.Valid() {
		return ""
	}
	return state.String()
}

func reviewEntryStampDeltaContext(ctx context.Context, oldState, oldRevision, oldSourceDigest, oldReviewDigest, state, revision string) (int, error) {
	delta := 0
	for _, values := range [][2]string{
		{oldState, state},
		{oldRevision, revision},
	} {
		oldSize, err := commandJSONStringSizeContext(ctx, values[0])
		if err != nil {
			return 0, err
		}
		newSize, err := commandJSONStringSizeContext(ctx, values[1])
		if err != nil {
			return 0, err
		}
		delta += newSize - oldSize
	}
	for _, digest := range []string{oldSourceDigest, oldReviewDigest} {
		oldSize, err := commandJSONStringSizeContext(ctx, digest)
		if err != nil {
			return 0, err
		}
		delta += 66 - oldSize
	}
	return delta, ctx.Err()
}

func commandJSONStringSizeContext(ctx context.Context, value string) (int, error) {
	if !utf8.ValidString(value) {
		return 0, fmt.Errorf("source string is not valid UTF-8")
	}
	size := 2
	for index := 0; index < len(value); {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		current := value[index]
		if current >= utf8.RuneSelf {
			r, width := utf8.DecodeRuneInString(value[index:])
			if r == '\u2028' || r == '\u2029' {
				size += 6
			} else {
				size += width
			}
			index += width
			continue
		}
		switch current {
		case '\\', '"', '\b', '\f', '\n', '\r', '\t':
			size += 2
		case '<', '>', '&':
			size += 6
		default:
			if current < 0x20 {
				size += 6
			} else {
				size++
			}
		}
		index++
	}
	return size, ctx.Err()
}

func cloneReviewCatalog(spec i18n.CatalogSpec) i18n.CatalogSpec {
	cloned, _ := cloneReviewCatalogContext(context.Background(), spec)
	return cloned
}

func cloneReviewCatalogContext(ctx context.Context, spec i18n.CatalogSpec) (i18n.CatalogSpec, error) {
	cloned := spec
	cloned.Supported = slices.Clone(spec.Supported)
	cloned.Required = slices.Clone(spec.Required)
	cloned.Parents = slices.Clone(spec.Parents)
	cloned.Capabilities = slices.Clone(spec.Capabilities)
	cloned.Overrides = slices.Clone(spec.Overrides)
	cloned.Modules = make([]i18n.Module, len(spec.Modules))
	for moduleIndex, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return i18n.CatalogSpec{}, err
		}
		cloned.Modules[moduleIndex] = module
		cloned.Modules[moduleIndex].Messages = make([]i18n.MessageSpec, len(module.Messages))
		for messageIndex, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return i18n.CatalogSpec{}, err
			}
			cloned.Modules[moduleIndex].Messages[messageIndex] = message
			clonedMessage := &cloned.Modules[moduleIndex].Messages[messageIndex]
			clonedMessage.Markup = slices.Clone(message.Markup)
			clonedMessage.Translations = slices.Clone(message.Translations)
			clonedMessage.Arguments = make([]i18n.ArgumentSpec, len(message.Arguments))
			for argumentIndex, argument := range message.Arguments {
				clonedMessage.Arguments[argumentIndex] = argument
				clonedMessage.Arguments[argumentIndex].Values = slices.Clone(argument.Values)
			}
		}
	}
	return cloned, ctx.Err()
}

func canonicalCommandLocale(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("locale is empty")
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid locale %q: %w", value, err)
	}
	canonical := tag.String()
	if canonical == "und" && value != "und" {
		return "", fmt.Errorf("invalid locale %q", value)
	}
	return canonical, nil
}

func parseReviewState(value string) (i18n.ReviewState, error) {
	switch value {
	case "approved":
		return i18n.ReviewApproved, nil
	case "required":
		return i18n.ReviewRequired, nil
	case "rejected":
		return i18n.ReviewRejected, nil
	default:
		return i18n.ReviewUnset, fmt.Errorf("unknown review state %q", value)
	}
}
