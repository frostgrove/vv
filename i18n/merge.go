package i18n

import (
	"context"
	"errors"
	"fmt"
)

var ErrMergeWouldDiscard = errors.New("i18n: source merge would discard previous work")

type SourceMergePolicy struct {
	PruneObsolete bool
}

type SourceMerger struct {
	CatalogLimits Limits
	SourceLimits  ArtifactLimits
}

func MergeSource(source, previous CatalogSpec, policy SourceMergePolicy) (CatalogSpec, error) {
	return (SourceMerger{}).Merge(source, previous, policy)
}

func MergeSourceContext(ctx context.Context, source, previous CatalogSpec, policy SourceMergePolicy) (CatalogSpec, error) {
	return (SourceMerger{}).MergeContext(ctx, source, previous, policy)
}

func (m SourceMerger) Merge(source, previous CatalogSpec, policy SourceMergePolicy) (CatalogSpec, error) {
	return m.MergeContext(context.Background(), source, previous, policy)
}

func (m SourceMerger) MergeContext(ctx context.Context, source, previous CatalogSpec, policy SourceMergePolicy) (CatalogSpec, error) {
	if ctx == nil {
		return CatalogSpec{}, fmt.Errorf("%w: merge context is nil", ErrInvalidSource)
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	ceiling, err := checkedLimits(m.CatalogLimits)
	if err != nil {
		return CatalogSpec{}, err
	}
	sourceLimits, err := checkedArtifactLimits(m.SourceLimits)
	if err != nil {
		return CatalogSpec{}, err
	}
	declared, err := checkedLimits(source.Limits)
	if err != nil {
		return CatalogSpec{}, err
	}
	if err := requireCatalogLimitCeiling(declared, ceiling); err != nil {
		return CatalogSpec{}, err
	}
	if err := checkSourceArtifactFloorContext(ctx, source, declared, sourceLimits); err != nil {
		return CatalogSpec{}, err
	}
	merged, err := canonicalSourceSpec(ctx, source, ceiling)
	if err != nil {
		return CatalogSpec{}, err
	}
	prior, err := canonicalSourceSpec(ctx, previous, ceiling)
	if err != nil {
		return CatalogSpec{}, err
	}
	if merged.SourceLocale != prior.SourceLocale {
		return CatalogSpec{}, fmt.Errorf("%w: source locale changed from %q to %q", ErrMergeWouldDiscard, prior.SourceLocale, merged.SourceLocale)
	}

	allowedLocales := sourceMergeLocales(merged)
	messages := make(map[Key]*MessageSpec)
	translations := make(map[Key]map[string]bool)
	for moduleIndex := range merged.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		module := &merged.Modules[moduleIndex]
		for messageIndex := range module.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			message := &module.Messages[messageIndex]
			messages[message.Key] = message
			locales := make(map[string]bool, len(message.Translations))
			for _, translation := range message.Translations {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				locales[translation.Locale] = true
			}
			translations[message.Key] = locales
		}
	}

	material, totalTranslations, err := pseudoInputMaterialContext(ctx, merged, merged.Limits)
	if err != nil {
		return CatalogSpec{}, err
	}
	var outputFloor artifactFloorCounter
	if err := checkSourceArtifactFloorIntoContext(ctx, merged, merged.Limits, sourceLimits, &outputFloor); err != nil {
		return CatalogSpec{}, err
	}
	translationIndexes := make(map[Key]int, len(messages))
	for key, message := range messages {
		translationIndexes[key] = len(message.Translations)
	}
	obsoleteCount := 0
	obsoleteFirst := ""
	noteObsolete := func(identity string) {
		obsoleteCount++
		if obsoleteFirst == "" || identity < obsoleteFirst {
			obsoleteFirst = identity
		}
	}
	limitError := func() error {
		if outputFloor.err != nil {
			return outputFloor.err
		}
		return fmt.Errorf("%w: merged source exceeds configured bounds", ErrLimitExceeded)
	}
	for _, module := range prior.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		for _, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			target := messages[message.Key]
			for _, translation := range message.Translations {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				if target == nil || !allowedLocales[translation.Locale] {
					noteObsolete(sourceMergeIdentity("translation", message.Key, translation.Locale))
					continue
				}
				if translations[message.Key][translation.Locale] {
					continue
				}
				if totalTranslations >= merged.Limits.MaxTranslations || !material.add(translation.Locale, translation.Text, translation.ContractRevision, translation.SourceDigest, translation.ReviewDigest) {
					return CatalogSpec{}, fmt.Errorf("%w: merged translations exceed configured bounds", ErrLimitExceeded)
				}
				encoded := sourceTranslation{
					Locale: translation.Locale, Text: translation.Text, Review: translation.Review.String(),
					ContractRevision: translation.ContractRevision, SourceDigest: translation.SourceDigest, ReviewDigest: translation.ReviewDigest,
				}
				if !outputFloor.addArrayValue(encoded, translationIndexes[message.Key], 7) {
					return CatalogSpec{}, limitError()
				}
				translationIndexes[message.Key]++
				totalTranslations++
			}
		}
	}

	overrides := make(map[string]bool, len(merged.Overrides))
	for _, override := range merged.Overrides {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		overrides[sourceMergePair(override.Key, override.Locale)] = true
	}
	overrideIndex := len(merged.Overrides)
	for _, override := range prior.Overrides {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		identity := sourceMergePair(override.Key, override.Locale)
		if overrides[identity] {
			continue
		}
		message := messages[override.Key]
		if message == nil || message.Override&OverrideApplication == 0 || !allowedLocales[override.Locale] {
			noteObsolete(sourceMergeIdentity("application override", override.Key, override.Locale))
			continue
		}
		if totalTranslations >= merged.Limits.MaxTranslations || !material.add(string(override.Key), override.Locale, override.Text, override.ContractRevision, override.SourceDigest, override.ReviewDigest) {
			return CatalogSpec{}, fmt.Errorf("%w: merged overrides exceed configured bounds", ErrLimitExceeded)
		}
		encoded := sourceOverride{
			Layer: LayerApplication.String(), Key: string(override.Key), Locale: override.Locale,
			Text: override.Text, Review: override.Review.String(), ContractRevision: override.ContractRevision,
			SourceDigest: override.SourceDigest, ReviewDigest: override.ReviewDigest,
		}
		if !outputFloor.addArrayValue(encoded, overrideIndex, 3) {
			return CatalogSpec{}, limitError()
		}
		overrideIndex++
		totalTranslations++
	}

	if obsoleteCount != 0 && !policy.PruneObsolete {
		return CatalogSpec{}, fmt.Errorf("%w: %d obsolete entries; first is %s", ErrMergeWouldDiscard, obsoleteCount, obsoleteFirst)
	}
	for _, module := range prior.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		for _, message := range module.Messages {
			target := messages[message.Key]
			for _, translation := range message.Translations {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				if target == nil || !allowedLocales[translation.Locale] || translations[message.Key][translation.Locale] {
					continue
				}
				target.Translations = append(target.Translations, translation)
				translations[message.Key][translation.Locale] = true
			}
		}
	}
	for _, override := range prior.Overrides {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		identity := sourceMergePair(override.Key, override.Locale)
		if overrides[identity] {
			continue
		}
		message := messages[override.Key]
		if message == nil || message.Override&OverrideApplication == 0 || !allowedLocales[override.Locale] {
			continue
		}
		merged.Overrides = append(merged.Overrides, override)
		overrides[identity] = true
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	return canonicalSourceSpec(ctx, merged, ceiling)
}

func sourceMergeLocales(spec CatalogSpec) map[string]bool {
	locales := make(map[string]bool, len(spec.Supported)+len(spec.Parents)*2+2)
	locales[spec.SourceLocale] = true
	locales[spec.DefaultLocale] = true
	for _, locale := range spec.Supported {
		locales[locale] = true
	}
	for _, edge := range spec.Parents {
		locales[edge.Locale] = true
		locales[edge.Parent] = true
	}
	return locales
}

func sourceMergePair(key Key, locale string) string {
	return string(key) + "\x00" + locale
}

func sourceMergeIdentity(kind string, key Key, locale string) string {
	return fmt.Sprintf("%s %q at locale %q", kind, key, locale)
}
