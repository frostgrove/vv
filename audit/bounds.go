package audit

import "time"

const (
	MaxNameBytes                     = 128
	MaxReferenceBytes                = 2048
	MaxIdempotencyKeyBytes           = 512
	MaxNarrativeBytes                = 4096
	MaxActorHops                     = 8
	MaxObservers                     = 8
	MaxItems                         = 256
	MaxFieldsPerItem                 = 256
	MaxValueBytes                    = 1 << 20
	MaxRevisionBytes                 = 16 << 20
	MaxAppendRequestBytes            = 40 << 20
	MaxPageRevisions                 = 1000
	MaxPageBytes                     = 32 << 20
	MaxStorePosition                 = 4096
	MaxCursorBytes                   = 8192
	MaxCursorLifetime                = 24 * time.Hour
	MaxRetryTokenBytes               = 40 << 20
	MaxReconcileKeyBytes             = 4096
	MaxCatalogs                      = 1024
	MaxCatalogDeclarations           = 65_536
	MaxCatalogManifestBytes          = 16 << 20
	MaxCatalogSetBytes               = 128 << 20
	MaxCatalogMutations              = 2048
	MaxCatalogMutationBytes          = 4 << 20
	MaxCatalogActivationAttemptTypes = 4096
	MaxCatalogActivationProofBytes   = 128 << 20
	MaxOperationMembers              = 256
	MaxCodesPerDeclaration           = 256
	MaxPolicyGoldens                 = 256
	MaxCodecFixtures                 = 1024
	MaxCodecFixtureBytes             = 1 << 20
	MaxCodecFixtureSetBytes          = 16 << 20
	MaxQueryResources                = 256
	MaxQueryActions                  = 256
	MaxQueryClassifications          = 256
	MaxQueryCoordinates              = 512
	MaxQuerySelectors                = 16
	MaxSelectorAlternatives          = 64
	MaxExactTargets                  = 10_000
	MaxQueryWindow                   = 100 * 365 * 24 * time.Hour
	MaxAttemptOpenLifetime           = 365 * 24 * time.Hour
	MaxAttemptSettlementTimeout      = time.Minute
	MaxAttemptCheckpoints            = 256
	MaxAttemptTransitions            = 259
	MaxAttemptStateBytes             = 4 << 20
	MaxRetentionCohortMembers        = MaxAttemptTransitions
	MaxHoldsPerRevision              = 256
	MaxHoldTransitionsPerRevision    = 512
	MaxHoldStateBytes                = 4 << 20
	MaxInventoryCandidates           = 10_000
	MaxInventoryCohorts              = 10_000
	MaxInventoryResultBytes          = 32 << 20
	MaxInventoryFenceBytes           = 8 << 20
	MaxInventoryCursorBytes          = 8192
	MaxSnapshotBytes                 = 8 << 20
	MaxSearchCohortRevisions         = 1_000_000
	MaxSearchCohortBytes             = 256 << 20
	MaxFenceLifetime                 = 24 * time.Hour
	MaxControlCursorLifetime         = 24 * time.Hour
)
