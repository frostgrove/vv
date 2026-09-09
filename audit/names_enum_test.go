package audit

import "testing"

type primitiveEnum interface {
	Valid() bool
	String() string
}

func TestPrimitivesClosedEnumsHaveStableLabels(t *testing.T) {
	valid := []struct {
		value primitiveEnum
		label string
	}{
		{Public, "public"}, {Internal, "internal"}, {Personal, "personal"}, {Secret, "secret"},
		{Required, "required"}, {BestEffort, "best_effort"},
		{ContextRequired, "required"}, {ContextOptional, "optional"},
		{HumanActor, "human"}, {WorkloadActor, "workload"}, {ServiceActor, "service"}, {SystemActor, "system"}, {ExternalActor, "external"},
		{EventItem, "event"}, {EntityItem, "entity"}, {AttemptItem, "attempt"}, {AccessItem, "access"}, {LifecycleItem, "lifecycle"},
		{ActorChainContext, "actor_chain"}, {ScopeContext, "scope"}, {ServiceContext, "service"}, {DeploymentContext, "deployment"}, {ClientContext, "client"},
		{OperationContext, "operation"}, {CorrelationContext, "correlation"}, {CausationContext, "causation"}, {TraceContext, "trace"}, {SourceContext, "source"},
		{AsPlaintext, "plaintext"}, {AsRedacted, "redacted"}, {AsToken, "token"}, {AsProtected, "protected"}, {AsIndexedProtected, "indexed_protected"},
		{UnstatedProvenance, "unstated"}, {ServerDerived, "server_derived"}, {Verified, "verified"}, {Forwarded, "forwarded"}, {ClientSupplied, "client_supplied"},
		{SupportUnstated, "unstated"}, {SupportUnsupported, "unsupported"}, {SupportSupported, "supported"},
		{Unclassified, "unclassified"}, {Conflict, "conflict"}, {Missing, "missing"}, {Corrupt, "corrupt"}, {NotWritten, "not_written"}, {Unconfirmed, "unconfirmed"},
		{Closed, "closed"}, {BadPosition, "bad_position"}, {StaleCatalog, "stale_catalog"}, {Refused, "refused"},
		{CryptoUnclassified, "unclassified"}, {CryptoInvalid, "invalid"}, {CryptoMissingKey, "missing_key"}, {CryptoUnsupported, "unsupported"},
		{CryptoMalformed, "malformed"}, {CryptoBackend, "backend"}, {CryptoRefused, "refused"},
		{DenialEvidenceNotConfigured, "not_configured"}, {DenialEvidenceSuppressed, "suppressed"}, {DenialEvidenceLimiterFailed, "limiter_failed"},
		{DenialEvidenceNotWritten, "not_written"}, {DenialEvidenceUnconfirmed, "unconfirmed"}, {DenialEvidenceCommitted, "committed"},
	}
	for _, one := range valid {
		if !one.value.Valid() || one.value.String() != one.label {
			t.Fatalf("enum %T renders %q with validity %v, want %q and valid", one.value, one.value.String(), one.value.Valid(), one.label)
		}
	}
}

func TestPrimitivesClosedEnumsRejectUnknownValues(t *testing.T) {
	unknown := []primitiveEnum{
		Classification(255), Consequence(255), ContextPresence(255), ActorKind(255), ItemKind(255), ContextFactKind(255),
		StorageMode(255), Provenance(255), Support(255), StoreOutcome(255), CryptoOutcome(255), DenialEvidenceState(255),
	}
	for _, value := range unknown {
		if value.Valid() || value.String() != "unknown" && value.String() != "unclassified" {
			t.Fatalf("unknown %T renders %q with validity %v", value, value.String(), value.Valid())
		}
	}
}
