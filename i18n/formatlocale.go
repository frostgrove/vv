package i18n

import (
	"context"
	"fmt"

	"github.com/agentable/go-intl/datetimeformat"
	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/numberformat"
	"github.com/agentable/go-intl/pluralrules"
)

type formatRequirements uint8

const (
	formatNumber formatRequirements = 1 << iota
	formatDate
	formatPlural
)

func validateFormatRequirements(localeName string, requirements formatRequirements) error {
	locales, err := locale.ParseList(localeName)
	if err != nil {
		return err
	}
	if requirements&formatNumber != 0 {
		formatter, err := numberformat.New(locales, numberformat.Options{})
		if err != nil {
			return err
		}
		if err := validateResolvedNumberLocale(localeName, formatter.ResolvedOptions(), nil); err != nil {
			return fmt.Errorf("number formatting: %w", err)
		}
	}
	if requirements&formatDate != 0 {
		zone := "UTC"
		formatter, err := datetimeformat.New(locales, datetimeformat.Options{TimeZone: &zone})
		if err != nil {
			return err
		}
		if err := validateResolvedDateLocale(localeName, formatter.ResolvedOptions(), nil); err != nil {
			return fmt.Errorf("date formatting: %w", err)
		}
	}
	if requirements&formatPlural != 0 {
		rules, err := pluralrules.New(locales, pluralrules.Options{})
		if err != nil {
			return err
		}
		if err := requireResolvedLocale(localeName, rules.ResolvedOptions().Locale); err != nil {
			return fmt.Errorf("plural rules: %w", err)
		}
	}
	return nil
}

func requireResolvedLocale(requested string, resolved locale.Locale) error {
	requestedLocale, err := locale.Parse(requested)
	if err != nil {
		return err
	}
	if requestedLocale.BaseName() != resolved.BaseName() {
		return fmt.Errorf("locale %q resolves to %q", requestedLocale.BaseName(), resolved.BaseName())
	}
	return nil
}

func validateResolvedNumberLocale(requested string, resolved numberformat.ResolvedOptions, options map[string]any) error {
	if err := requireResolvedLocale(requested, resolved.Locale); err != nil {
		return err
	}
	requestedLocale, err := locale.Parse(requested)
	if err != nil {
		return err
	}
	numberingSystem := requestedLocale.NumberingSystem()
	if configured, exists := optionString(options, "numberingSystem"); exists {
		numberingSystem = configured
	}
	if numberingSystem != "" && resolved.NumberingSystem != numberingSystem {
		return fmt.Errorf("numbering system %q resolves to %q", numberingSystem, resolved.NumberingSystem)
	}
	return nil
}

func validateResolvedDateLocale(requested string, resolved datetimeformat.ResolvedOptions, options map[string]any) error {
	if err := requireResolvedLocale(requested, resolved.Locale); err != nil {
		return err
	}
	requestedLocale, err := locale.Parse(requested)
	if err != nil {
		return err
	}
	calendar := requestedLocale.Calendar()
	if configured, exists := optionString(options, "calendar"); exists {
		calendar = configured
	}
	if calendar != "" && resolved.Calendar != calendar {
		return fmt.Errorf("calendar %q resolves to %q", calendar, resolved.Calendar)
	}
	numberingSystem := requestedLocale.NumberingSystem()
	if configured, exists := optionString(options, "numberingSystem"); exists {
		numberingSystem = configured
	}
	if numberingSystem != "" && resolved.NumberingSystem != numberingSystem {
		return fmt.Errorf("numbering system %q resolves to %q", numberingSystem, resolved.NumberingSystem)
	}
	hourCycle := requestedLocale.HourCycle()
	if configured, exists := optionString(options, "hourCycle"); exists {
		hourCycle = configured
	}
	if hourCycle != "" && resolved.HourCycle != nil && string(*resolved.HourCycle) != hourCycle {
		return fmt.Errorf("hour cycle %q resolves to %q", hourCycle, *resolved.HourCycle)
	}
	return nil
}

func snapshotFormattingRequirements(snapshot *Snapshot) (map[string]formatRequirements, error) {
	return snapshotFormattingRequirementsContext(context.Background(), snapshot)
}

func snapshotFormattingRequirementsContext(ctx context.Context, snapshot *Snapshot) (map[string]formatRequirements, error) {
	requirements := make(map[string]formatRequirements, len(snapshot.supported))
	for _, localeName := range snapshot.supported {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chain := snapshot.fallbackChain(localeName)
		var localeRequirements formatRequirements
		for _, record := range snapshot.records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, candidate := range chain {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				translation, exists := record.templates[candidate]
				if !exists {
					continue
				}
				localeRequirements |= translation.work.requirements & (formatNumber | formatDate)
				break
			}
		}
		if err := validateFormatRequirements(localeName, localeRequirements); err != nil {
			return nil, fmt.Errorf("%w: formatting locale %q: %v", ErrInvalidLocale, localeName, err)
		}
		requirements[localeName] = localeRequirements
	}
	return requirements, ctx.Err()
}
