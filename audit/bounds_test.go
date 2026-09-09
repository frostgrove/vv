package audit

import (
	"testing"
	"time"
)

func TestPrimitivesPublishTheFrozenBounds(t *testing.T) {
	numbers := []struct {
		name string
		got  int
		want int
	}{
		{"MaxNameBytes", MaxNameBytes, 128},
		{"MaxReferenceBytes", MaxReferenceBytes, 2048},
		{"MaxIdempotencyKeyBytes", MaxIdempotencyKeyBytes, 512},
		{"MaxNarrativeBytes", MaxNarrativeBytes, 4096},
		{"MaxActorHops", MaxActorHops, 8},
		{"MaxObservers", MaxObservers, 8},
		{"MaxItems", MaxItems, 256},
		{"MaxFieldsPerItem", MaxFieldsPerItem, 256},
		{"MaxValueBytes", MaxValueBytes, 1 << 20},
		{"MaxRevisionBytes", MaxRevisionBytes, 16 << 20},
		{"MaxAppendRequestBytes", MaxAppendRequestBytes, 40 << 20},
		{"MaxPageRevisions", MaxPageRevisions, 1000},
		{"MaxPageBytes", MaxPageBytes, 32 << 20},
		{"MaxStorePosition", MaxStorePosition, 4096},
		{"MaxCursorBytes", MaxCursorBytes, 8192},
		{"MaxRetryTokenBytes", MaxRetryTokenBytes, 40 << 20},
		{"MaxReconcileKeyBytes", MaxReconcileKeyBytes, 4096},
		{"MaxCatalogs", MaxCatalogs, 1024},
		{"MaxCatalogDeclarations", MaxCatalogDeclarations, 65_536},
		{"MaxCatalogManifestBytes", MaxCatalogManifestBytes, 16 << 20},
		{"MaxCatalogSetBytes", MaxCatalogSetBytes, 128 << 20},
		{"MaxCatalogMutations", MaxCatalogMutations, 2048},
		{"MaxCatalogMutationBytes", MaxCatalogMutationBytes, 4 << 20},
		{"MaxCatalogActivationAttemptTypes", MaxCatalogActivationAttemptTypes, 4096},
		{"MaxCatalogActivationProofBytes", MaxCatalogActivationProofBytes, 128 << 20},
		{"MaxOperationMembers", MaxOperationMembers, 256},
		{"MaxCodesPerDeclaration", MaxCodesPerDeclaration, 256},
		{"MaxPolicyGoldens", MaxPolicyGoldens, 256},
		{"MaxCodecFixtures", MaxCodecFixtures, 1024},
		{"MaxCodecFixtureBytes", MaxCodecFixtureBytes, 1 << 20},
		{"MaxCodecFixtureSetBytes", MaxCodecFixtureSetBytes, 16 << 20},
		{"MaxQueryResources", MaxQueryResources, 256},
		{"MaxQueryActions", MaxQueryActions, 256},
		{"MaxQueryClassifications", MaxQueryClassifications, 256},
		{"MaxQueryCoordinates", MaxQueryCoordinates, 512},
		{"MaxQuerySelectors", MaxQuerySelectors, 16},
		{"MaxSelectorAlternatives", MaxSelectorAlternatives, 64},
		{"MaxExactTargets", MaxExactTargets, 10_000},
		{"MaxAttemptCheckpoints", MaxAttemptCheckpoints, 256},
		{"MaxAttemptTransitions", MaxAttemptTransitions, 259},
		{"MaxAttemptStateBytes", MaxAttemptStateBytes, 4 << 20},
		{"MaxRetentionCohortMembers", MaxRetentionCohortMembers, 259},
		{"MaxHoldsPerRevision", MaxHoldsPerRevision, 256},
		{"MaxHoldTransitionsPerRevision", MaxHoldTransitionsPerRevision, 512},
		{"MaxHoldStateBytes", MaxHoldStateBytes, 4 << 20},
		{"MaxInventoryCandidates", MaxInventoryCandidates, 10_000},
		{"MaxInventoryCohorts", MaxInventoryCohorts, 10_000},
		{"MaxInventoryResultBytes", MaxInventoryResultBytes, 32 << 20},
		{"MaxInventoryFenceBytes", MaxInventoryFenceBytes, 8 << 20},
		{"MaxInventoryCursorBytes", MaxInventoryCursorBytes, 8192},
		{"MaxSnapshotBytes", MaxSnapshotBytes, 8 << 20},
		{"MaxSearchCohortRevisions", MaxSearchCohortRevisions, 1_000_000},
		{"MaxSearchCohortBytes", MaxSearchCohortBytes, 256 << 20},
	}
	for _, number := range numbers {
		if number.got != number.want {
			t.Fatalf("%s is %d, want %d", number.name, number.got, number.want)
		}
	}
	durations := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"MaxCursorLifetime", MaxCursorLifetime, 24 * time.Hour},
		{"MaxQueryWindow", MaxQueryWindow, 100 * 365 * 24 * time.Hour},
		{"MaxAttemptOpenLifetime", MaxAttemptOpenLifetime, 365 * 24 * time.Hour},
		{"MaxAttemptSettlementTimeout", MaxAttemptSettlementTimeout, time.Minute},
		{"MaxFenceLifetime", MaxFenceLifetime, 24 * time.Hour},
		{"MaxControlCursorLifetime", MaxControlCursorLifetime, 24 * time.Hour},
	}
	for _, duration := range durations {
		if duration.got != duration.want {
			t.Fatalf("%s is %s, want %s", duration.name, duration.got, duration.want)
		}
	}
}
