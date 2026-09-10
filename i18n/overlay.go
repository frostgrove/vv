package i18n

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"unicode/utf8"
)

type Override struct {
	Key              Key
	Locale           string
	Text             string
	ContractRevision string
	SourceDigest     string
	ReviewDigest     string
	Review           ReviewState
}

type OverlaySpec struct {
	Layer     Layer
	Revision  string
	Overrides []Override
}

func ApplicationOverlay(revision string, overrides ...Override) OverlaySpec {
	return OverlaySpec{Layer: LayerApplication, Revision: revision, Overrides: slices.Clone(overrides)}
}

func TenantOverlay(revision string, overrides ...Override) OverlaySpec {
	return OverlaySpec{Layer: LayerTenant, Revision: revision, Overrides: slices.Clone(overrides)}
}

func (s *Snapshot) Overlay(spec OverlaySpec) (*Snapshot, error) {
	return s.overlayContext(context.Background(), spec, true)
}

func (s *Snapshot) overlay(spec OverlaySpec, appendRevision bool) (*Snapshot, error) {
	return s.overlayContext(context.Background(), spec, appendRevision)
}

func (s *Snapshot) overlayContext(ctx context.Context, spec OverlaySpec, appendRevision bool) (*Snapshot, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: overlay context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: snapshot is nil", ErrInvalidCatalog)
	}
	problems := &problemSet{}
	if spec.Layer != LayerApplication && spec.Layer != LayerTenant {
		problems.add(ProblemInvalid, "overlay.layer", "only application or tenant layers are accepted")
	} else if spec.Layer <= s.highestLayer {
		problems.add(ProblemSecurity, "overlay.layer", "overlay layers must strictly increase")
	}
	if spec.Revision == "" {
		problems.add(ProblemMissing, "overlay.revision", "overlay revision is required")
	} else if len(spec.Revision) > s.limits.MaxRevisionBytes {
		problems.add(ProblemLimit, "overlay.revision", strconv.Itoa(len(spec.Revision)))
	} else if !utf8.ValidString(spec.Revision) {
		problems.add(ProblemInvalid, "overlay.revision", "overlay revision is not valid UTF-8")
	}
	if len(spec.Overrides) > s.limits.MaxTranslations {
		problems.add(ProblemLimit, "overlay.overrides", strconv.Itoa(len(spec.Overrides)))
	}
	if err := problems.err(); err != nil {
		return nil, err
	}
	counter := artifactCounter{maxBytes: s.limits.MaxCatalogBytes, maxItems: s.limits.MaxCatalogItems}
	if !counter.add(spec.Revision) {
		problems.add(ProblemLimit, "overlay", "overlay material exceeds configured bounds")
		return nil, problems.err()
	}
	for i, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !counter.add(string(override.Key), override.Locale, override.Text, override.ContractRevision, override.SourceDigest, override.ReviewDigest) {
			problems.add(ProblemLimit, problemPath("overlay.overrides[%d]", i), "overlay material exceeds configured bounds")
			return nil, problems.err()
		}
	}

	records := make(map[Key]*messageRecord, len(s.records))
	totalTranslations := 0
	for key, record := range s.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		templates := maps.Clone(record.templates)
		descriptor, err := cloneDescriptorContext(ctx, record.descriptor)
		if err != nil {
			return nil, err
		}
		totalTranslations += len(templates)
		records[key] = &messageRecord{
			descriptor:   descriptor,
			contractHash: record.contractHash,
			sourceDigest: record.sourceDigest,
			allowEmpty:   record.allowEmpty,
			templates:    templates,
		}
	}
	allowedLocales := make(map[string]bool, len(s.supported)+len(s.parents)+2)
	for _, locale := range s.supported {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		allowedLocales[locale] = true
	}
	allowedLocales[s.sourceLocale] = true
	allowedLocales[s.defaultLocale] = true
	for child, parent := range s.parents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		allowedLocales[child] = true
		allowedLocales[parent] = true
	}

	seen := make(map[string]string, len(spec.Overrides))
	for i, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := problemPath("overlay.overrides[%d]", i)
		record, ok := records[override.Key]
		if !ok {
			problems.add(ProblemMissing, path+".key", string(override.Key))
			continue
		}
		if !overrideAllowed(record.descriptor.Override, spec.Layer) {
			problems.add(ProblemSecurity, path+".key", "message does not allow this layer")
			continue
		}
		_, locale, err := canonicalLocale(override.Locale, s.limits.Locale.MaxTagBytes)
		if err != nil {
			problems.addError(ProblemInvalid, path+".locale", err)
			continue
		}
		identity := string(override.Key) + "\x00" + locale
		if raw, exists := seen[identity]; exists {
			code := ProblemDuplicate
			if raw != override.Locale {
				code = ProblemCollision
			}
			problems.add(code, path+".locale", locale)
			continue
		}
		seen[identity] = override.Locale
		if !allowedLocales[locale] {
			problems.add(ProblemInvalid, path+".locale", "locale is outside the declared graph")
			continue
		}
		if override.ContractRevision == "" || override.ContractRevision != record.descriptor.Revision {
			problems.add(ProblemStale, path+".contract_revision", override.ContractRevision)
			continue
		}
		if override.Review == ReviewUnset {
			problems.add(ProblemMissing, path+".review", "override review is required")
			continue
		}
		if override.Review != ReviewApproved {
			problems.add(ProblemStale, path+".review", "override is not approved")
			continue
		}
		if override.SourceDigest == "" {
			problems.add(ProblemMissing, path+".source_digest", "override source digest is required")
			continue
		}
		if override.SourceDigest != record.sourceDigest {
			problems.add(ProblemStale, path+".source_digest", override.SourceDigest)
			continue
		}
		text := override.Text
		if text == "" && !record.allowEmpty {
			problems.add(ProblemMissing, path+".text", "empty override is not allowed")
			continue
		}
		if len(text) > s.limits.MaxTemplateBytes {
			problems.add(ProblemLimit, path+".text", strconv.Itoa(len(text)))
			continue
		}
		if !utf8.ValidString(text) {
			problems.add(ProblemInvalid, path+".text", "override is not valid UTF-8")
			continue
		}
		if hasUnsafeAuthoredBidiControls(text) {
			problems.add(ProblemSecurity, path+".text", "override contains directional controls")
			continue
		}
		expectedReviewDigest, digestErr := ExpectedReviewDigest(record.sourceDigest, locale, text)
		if digestErr != nil {
			problems.addError(ProblemInvalid, path+".review_digest", digestErr)
			continue
		}
		if override.ReviewDigest == "" {
			problems.add(ProblemMissing, path+".review_digest", "override content review digest is required")
			continue
		}
		if override.ReviewDigest != expectedReviewDigest {
			problems.add(ProblemStale, path+".review_digest", override.ReviewDigest)
			continue
		}
		if previous, exists := record.templates[locale]; exists && previous.layer > spec.Layer {
			problems.add(ProblemSecurity, path+".key", "a lower layer cannot replace a higher layer")
			continue
		}
		_, replaces := record.templates[locale]
		if !replaces && totalTranslations >= s.limits.MaxTranslations {
			problems.add(ProblemLimit, path+".locale", "overlay exceeds the configured translation limit")
			continue
		}
		compiled, ok := compileTranslation(locale, spec.Layer, text, record.descriptor, s.capabilities, s.limits, path+".text", problems)
		if ok {
			record.templates[locale] = compiled
			if !replaces {
				totalTranslations++
			}
		}
	}

	revision := s.revision
	if appendRevision {
		revision += "+" + spec.Revision
	}
	if len(revision) > s.limits.MaxRevisionBytes {
		problems.add(ProblemLimit, "overlay.revision", "combined revision exceeds "+strconv.Itoa(s.limits.MaxRevisionBytes)+" bytes")
	}
	clone := &Snapshot{
		revision:            revision,
		profile:             s.profile,
		sourceLocale:        s.sourceLocale,
		defaultLocale:       s.defaultLocale,
		defaultTimeZone:     s.defaultTimeZone,
		timeZoneDataVersion: s.timeZoneDataVersion,
		supported:           slices.Clone(s.supported),
		required:            slices.Clone(s.required),
		parents:             maps.Clone(s.parents),
		records:             records,
		resolver:            s.resolver,
		capabilities:        maps.Clone(s.capabilities),
		matchMode:           s.matchMode,
		defaultOnMiss:       s.defaultOnMiss,
		limits:              s.limits,
		observer:            s.observer,
		highestLayer:        spec.Layer,
	}
	formatRequirements, err := snapshotFormattingRequirementsContext(ctx, clone)
	if err != nil {
		return nil, err
	}
	clone.formatRequirements = formatRequirements
	bytes, items, err := snapshotCatalogMaterialContext(ctx, clone)
	if err != nil {
		return nil, err
	}
	if bytes > s.limits.MaxCatalogBytes {
		problems.add(ProblemLimit, "overlay.catalog_bytes", strconv.Itoa(bytes))
	}
	if items > s.limits.MaxCatalogItems {
		problems.add(ProblemLimit, "overlay.catalog_items", strconv.Itoa(items))
	}
	if err := problems.err(); err != nil {
		return nil, err
	}
	clone.digest, err = snapshotDigestContext(ctx, clone)
	if err != nil {
		return nil, err
	}
	return clone, nil
}

func overrideAllowed(policy OverridePolicy, layer Layer) bool {
	switch layer {
	case LayerApplication:
		return policy&OverrideApplication != 0
	case LayerTenant:
		return policy&OverrideTenant != 0
	default:
		return false
	}
}
