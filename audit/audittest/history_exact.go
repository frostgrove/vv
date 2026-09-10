package audittest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

type exactLogMode uint8

const (
	exactLogNormal exactLogMode = iota
	exactLogEmptyResult
	exactLogReplayResult
	exactLogBadSeal
)

type trackingExactLog struct {
	audit.ExactLog
	mu                sync.Mutex
	calls             int
	mode              exactLogMode
	cached            audit.ExactResult
	itemEnvelopeItems int
}

func (l *trackingExactLog) Inspect(ctx context.Context, query audit.ExactQuery) (audit.ExactResult, error) {
	if ctx.Value(callerValueKey{}) != nil {
		return audit.ExactResult{}, audit.Failure(audit.Refused, errors.New("audittest: exact store received caller value"))
	}
	l.mu.Lock()
	l.calls++
	mode := l.mode
	if mode == exactLogReplayResult && len(l.cached.Entries()) != 0 {
		cached := l.cached
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()
	if mode == exactLogEmptyResult {
		return audit.ExactResult{}, nil
	}
	result, err := l.ExactLog.Inspect(ctx, query)
	if err != nil {
		return result, err
	}
	view := query.View()
	if len(view.Targets) == 1 && view.Targets[0].Kind == audit.ExactItemTarget {
		for _, entry := range result.Entries() {
			if entry.State == audit.ExactFound {
				l.mu.Lock()
				l.itemEnvelopeItems = len(entry.Revision.View().Revision.Items)
				l.mu.Unlock()
			}
		}
	}
	if mode == exactLogBadSeal {
		entries := result.Entries()
		for index, entry := range entries {
			if entry.State != audit.ExactFound {
				continue
			}
			view := entry.Revision.View()
			seal := view.Revision.Header.Seal
			view.Revision.Header.Seal, err = audit.NewSeal(seal.Algorithm(), seal.Profile(), seal.KeyID(), bytes.Repeat([]byte{0xfe}, 32))
			if err != nil {
				return audit.ExactResult{}, audit.Failure(audit.Corrupt, err)
			}
			entries[index].Revision, err = audit.NewStoredRevision(audit.StoredRevisionData{
				Revision: view.Revision, RecordedAt: view.RecordedAt, Position: view.Position,
				ActiveHoldCount: view.ActiveHoldCount, HoldEpoch: view.HoldEpoch,
				ActiveHoldSet: view.ActiveHoldSet, HoldTransitions: view.HoldTransitions,
			})
			if err != nil {
				return audit.ExactResult{}, audit.Failure(audit.Corrupt, err)
			}
		}
		return audit.NewExactResult(query, audit.ExactResultData{Entries: entries})
	}
	if mode == exactLogReplayResult {
		l.mu.Lock()
		l.cached = result
		l.mu.Unlock()
	}
	return result, nil
}

func (l *trackingExactLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *trackingExactLog) itemEnvelopeSize() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.itemEnvelopeItems
}

type exactCapabilityLog struct {
	audit.ExactLog
	capabilities   audit.Capabilities
	limits         audit.Limits
	overrideLimits bool
}

type exactForeignLog struct{ audit.ExactLog }

func (l exactForeignLog) LogID() audit.LogID {
	result := l.ExactLog.LogID()
	result[0]++
	return result
}

type exactForeignCatalog struct {
	audit.ExactLog
	state audit.StoreCatalogState
}

func (l exactForeignCatalog) Catalogs() audit.StoreCatalogState { return l.state }

func (l exactCapabilityLog) Capabilities() audit.Capabilities { return l.capabilities }

func (l exactCapabilityLog) Limits() audit.Limits {
	if l.overrideLimits {
		return l.limits
	}
	return l.ExactLog.Limits()
}

func ExactHistory(t *testing.T, factory HistoryStoreFactory) {
	t.Helper()
	if factory == nil {
		t.Fatal("nil exact history store factory")
	}
	ctx := context.Background()
	policy := audit.ContextFacts(
		audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	sent := declareHistoryEvent("invoice.exact", policy)
	canceled := declareHistoryEvent("invoice.exact.canceled", policy)
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "billing.invoice.exact", Semantics: audit.Semantics(1), Retention: "business.forever",
		Consequence: audit.Required, Context: policy, Members: audit.OperationMembers(sent, canceled),
	})
	semantic, err := audit.HMACSemanticDigester("exact-semantic", bytes.Repeat([]byte{11}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "exact-identity", Key: bytes.Repeat([]byte{12}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := audit.HMACSigner(audit.HMACSigningKey{KeyID: "exact-signature", Key: bytes.Repeat([]byte{13}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "exact-signature", Key: bytes.Repeat([]byte{13}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "history.exact", Owner: "history.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.RequireSignature(signer.Description()),
	}, sent, canceled, operation)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	change, err := audit.NewCatalogChangeRef("exact.deploy", "audittest")
	if err != nil {
		t.Fatal(err)
	}
	clock := &sequenceClock{next: time.Date(2050, 1, 1, 0, 0, 0, 0, time.UTC)}
	opened, err := factory(ctx, HistoryStoreRequest{Catalogs: catalogs, Change: change, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Close != nil {
		t.Cleanup(func() {
			if err := opened.Close(); err != nil {
				t.Errorf("close exact history store: %v", err)
			}
		})
	}
	if opened.Writer == nil || opened.Log == nil || opened.Exact == nil {
		t.Fatal("exact history store factory returned nil contracts")
	}
	scope, err := audit.NewContextValue(audit.ScopedReference{Scope: "tenant", Reference: "north"}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver := audit.ContextResolverFunc(func(ctx context.Context) (audit.Context, error) {
		operationID, _ := ctx.Value(operationContextKey{}).(audit.OperationID)
		if operationID == (audit.OperationID{}) {
			operationID = audit.OperationID{201}
		}
		value, valueErr := audit.NewContextValue(operationID, audit.Verified)
		return audit.Context{Scope: scope, Operation: value}, valueErr
	})
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: opened.Writer, Context: resolver,
		Semantics: semantic, Identities: identities, Signer: signer, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	revisionID := recordExactHistoryGroup(t, recorder, operation,
		exactHistoryDraft(t, sent, historyEvent{Target: "invoice:exact-a", Value: "exact-a", At: time.Unix(21, 0).UTC()}),
		exactHistoryDraft(t, canceled, historyEvent{Target: "invoice:exact-b", Value: "exact-b", At: time.Unix(22, 0).UTC()}),
	)
	reference := audit.RevisionRef{Catalog: catalog.Ref(), Revision: revisionID}
	tracked := &trackingExactLog{ExactLog: opened.Exact}
	var authorityCalls atomic.Int64
	authority := audit.AccessAuthorityFunc(func(ctx context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
		if ctx.Value(callerValueKey{}) != nil {
			return audit.AccessDecision{}, errors.New("audittest: exact authority received caller value")
		}
		authorityCalls.Add(1)
		return allowRequested(request)
	})
	basicOnly, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	access := exactHistoryAccess("invoice.exact", "invoice.exact.canceled")
	itemAccess := exactHistoryAccess("invoice.exact")
	if _, err := basicOnly.Revision(ctx, reference, access); !errors.Is(err, audit.ErrUnsupported) {
		t.Fatalf("exact inspection without exact log = %v", err)
	}
	unsupported := exactCapabilityLog{ExactLog: opened.Exact, capabilities: exactCapabilities(t, opened.Exact, audit.SupportUnsupported)}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: unsupported, Access: authority, Verifier: verifier,
	}); !errors.Is(err, audit.ErrUnsupported) {
		t.Fatalf("unsupported exact capability = %v", err)
	}
	zeroLimits := exactCapabilityLog{ExactLog: opened.Exact, capabilities: opened.Exact.Capabilities(), overrideLimits: true}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: zeroLimits, Access: authority, Verifier: verifier,
	}); !errors.Is(err, audit.ErrWrongStore) {
		t.Fatalf("zero exact limits = %v", err)
	}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: exactForeignLog{ExactLog: opened.Exact}, Access: authority, Verifier: verifier,
	}); !errors.Is(err, audit.ErrWrongStore) {
		t.Fatalf("foreign exact log = %v", err)
	}
	foreignState, err := audit.NewStoreCatalogState(catalog.Ref(), audit.CatalogSetDigest{99})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: exactForeignCatalog{ExactLog: opened.Exact, state: foreignState}, Access: authority, Verifier: verifier,
	}); !errors.Is(err, audit.ErrWrongCatalog) {
		t.Fatalf("foreign exact catalog = %v", err)
	}
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: tracked, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := context.WithValue(ctx, callerValueKey{}, "secret")
	revisionResult, err := history.Revision(caller, reference, access)
	if err != nil {
		t.Fatal(err)
	}
	revision := revisionResult.Revision()
	assertExactRevision(t, revision, reference, []string{"exact-a", "exact-b"})
	copyRevision := revisionResult.Revision()
	copyRevision.Items[0].Values[0].Canonical[0] = 'X'
	assertExactRevision(t, revisionResult.Revision(), reference, []string{"exact-a", "exact-b"})
	itemResult, err := history.Item(caller, audit.ItemRef{Revision: reference, Ordinal: 0}, itemAccess)
	if err != nil {
		t.Fatal(err)
	}
	if tracked.itemEnvelopeSize() != 2 {
		t.Fatalf("exact item store envelope = %d items, want complete sibling envelope", tracked.itemEnvelopeSize())
	}
	assertExactItem(t, itemResult.Item(), 0, "invoice:exact-a", "exact-a")
	if _, err := history.Revision(caller, reference, itemAccess); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("exact revision with an uncovered sibling = %v", err)
	}
	copyItem := itemResult.Item()
	copyItem.Values[0].Canonical[0] = 'X'
	assertExactItem(t, itemResult.Item(), 0, "invoice:exact-a", "exact-a")
	beforeMissingAuthority, beforeMissingStore := authorityCalls.Load(), tracked.count()
	missing := reference
	missing.Revision = audit.RevisionID{99}
	if _, err := history.Revision(caller, missing, access); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("missing exact revision = %v", err)
	}
	if authorityCalls.Load() != beforeMissingAuthority+1 || tracked.count() != beforeMissingStore+1 {
		t.Fatal("missing exact revision did not authorize before inspection")
	}
	beforeMissingOrdinalAuthority, beforeMissingOrdinalStore := authorityCalls.Load(), tracked.count()
	if _, err := history.Item(caller, audit.ItemRef{Revision: reference, Ordinal: 2}, access); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("missing exact ordinal = %v", err)
	}
	if authorityCalls.Load() != beforeMissingOrdinalAuthority+1 || tracked.count() != beforeMissingOrdinalStore+1 {
		t.Fatal("missing exact ordinal did not authorize before inspection")
	}
	beforeRefusalAuthority, beforeRefusalStore := authorityCalls.Load(), tracked.count()
	foreign := reference
	foreign.Catalog.Generation++
	if _, err := history.Revision(caller, foreign, access); !errors.Is(err, audit.ErrRefused) {
		t.Fatalf("foreign exact catalog = %v", err)
	}
	if _, err := history.Item(caller, audit.ItemRef{Revision: reference, Ordinal: audit.MaxItems}, access); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("invalid exact ordinal = %v", err)
	}
	if authorityCalls.Load() != beforeRefusalAuthority || tracked.count() != beforeRefusalStore {
		t.Fatal("invalid exact target reached authority or store")
	}
	for name, invalid := range map[string]audit.ExactAccessQuery{
		"resources":       {Classifications: []audit.Classification{audit.Public}, Query: access.Query},
		"actions":         {Resources: []audit.Resource{"billing.invoice"}, Classifications: []audit.Classification{audit.Public}, Query: exactHistoryQuery()},
		"classifications": {Resources: []audit.Resource{"billing.invoice"}, Query: access.Query},
	} {
		beforeAuthority, beforeStore := authorityCalls.Load(), tracked.count()
		if _, err := history.Revision(caller, reference, invalid); !errors.Is(err, audit.ErrInvalid) {
			t.Fatalf("empty exact %s ceiling = %v", name, err)
		}
		if authorityCalls.Load() != beforeAuthority || tracked.count() != beforeStore {
			t.Fatalf("empty exact %s ceiling reached authority or store", name)
		}
	}
	tooManyResources := access
	tooManyResources.Resources = make([]audit.Resource, audit.MaxQueryResources+1)
	beforeBoundAuthority, beforeBoundStore := authorityCalls.Load(), tracked.count()
	if _, err := history.Revision(caller, reference, tooManyResources); !errors.Is(err, audit.ErrTooLarge) {
		t.Fatalf("oversized exact resource ceiling = %v", err)
	}
	if authorityCalls.Load() != beforeBoundAuthority || tracked.count() != beforeBoundStore {
		t.Fatal("oversized exact resource ceiling reached authority or store")
	}
	canceledContext, cancel := context.WithCancel(caller)
	cancel()
	beforeCanceledAuthority, beforeCanceledStore := authorityCalls.Load(), tracked.count()
	if _, err := history.Revision(canceledContext, reference, access); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled exact revision = %v", err)
	}
	if authorityCalls.Load() != beforeCanceledAuthority || tracked.count() != beforeCanceledStore {
		t.Fatal("canceled exact revision reached authority or store")
	}
	var concurrent sync.WaitGroup
	concurrentErrors := make(chan error, 16)
	for index := 0; index < 16; index++ {
		concurrent.Add(1)
		go func(index int) {
			defer concurrent.Done()
			if index%2 == 0 {
				_, callErr := history.Revision(caller, reference, access)
				concurrentErrors <- callErr
				return
			}
			_, callErr := history.Item(caller, audit.ItemRef{Revision: reference, Ordinal: 0}, itemAccess)
			concurrentErrors <- callErr
		}(index)
	}
	concurrent.Wait()
	close(concurrentErrors)
	for callErr := range concurrentErrors {
		if callErr != nil {
			t.Fatalf("concurrent exact inspection = %v", callErr)
		}
	}
	denied, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: tracked, Verifier: verifier,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			return audit.DenyAccess(request, "policy.denied")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeDenied := tracked.count()
	if _, err := denied.Revision(caller, reference, access); !errors.Is(err, audit.ErrDenied) || tracked.count() != beforeDenied {
		t.Fatalf("denied exact revision = %v, calls=%d/%d", err, tracked.count(), beforeDenied)
	}
	oversized, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: tracked, Verifier: verifier,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			return allowExactWithBytes(request, opened.Exact.Limits().View().ExactBytes+1)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeOversized := tracked.count()
	if _, err := oversized.Revision(caller, reference, access); !errors.Is(err, audit.ErrDenied) || tracked.count() != beforeOversized {
		t.Fatalf("oversized exact grant = %v, calls=%d/%d", err, tracked.count(), beforeOversized)
	}
	emptyLog := &trackingExactLog{ExactLog: opened.Exact, mode: exactLogEmptyResult}
	emptyHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: emptyLog, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emptyHistory.Revision(caller, reference, access); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("empty hostile exact result = %v", err)
	}
	replayLog := &trackingExactLog{ExactLog: opened.Exact, mode: exactLogReplayResult}
	replayHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: replayLog, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replayHistory.Revision(caller, reference, access); err != nil {
		t.Fatal(err)
	}
	if _, err := replayHistory.Revision(caller, reference, access); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("replayed foreign-origin exact result = %v", err)
	}
	badSealLog := &trackingExactLog{ExactLog: opened.Exact, mode: exactLogBadSeal}
	badSealHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: badSealLog, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := badSealHistory.Revision(caller, reference, access); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("substituted exact signature = %v", err)
	}
	assertUnsignedExactHistory(t, factory, sent, canceled, operation, semantic, identities, resolver, access)
}

func exactHistoryAccess(actions ...audit.Action) audit.ExactAccessQuery {
	return audit.ExactAccessQuery{
		Resources: []audit.Resource{"billing.invoice"}, Classifications: []audit.Classification{audit.Public},
		Query: exactHistoryQuery(actions...),
	}
}

func exactHistoryDraft(t *testing.T, event *audit.EventType[historyEvent], value historyEvent) audit.Draft {
	t.Helper()
	draft, err := event.New(value)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func recordExactHistoryGroup(t *testing.T, recorder *audit.Recorder, operation *audit.OperationType, drafts ...audit.Draft) audit.RevisionID {
	t.Helper()
	ctx := context.WithValue(context.Background(), operationContextKey{}, audit.OperationID{21})
	result, err := recorder.Within(ctx, audit.GroupSpec{Operation: operation}, func(groupContext context.Context) error {
		for _, draft := range drafts {
			if err := recorder.Stage(groupContext, draft); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := result.Receipt()
	if !found {
		t.Fatal("exact history group has no receipt")
	}
	return receipt.RevisionID()
}

func exactHistoryQuery(actions ...audit.Action) audit.Query {
	query := audit.Query{
		Purpose: "history.read", Role: "auditor", Scope: audit.CurrentScope(),
		Fields: audit.AllFields(), Context: audit.AllContext(),
	}
	query.Actions = slices.Clone(actions)
	return query
}

func exactCapabilities(t *testing.T, exact audit.ExactLog, support audit.Support) audit.Capabilities {
	t.Helper()
	view := exact.Capabilities().View()
	view.ExactInspection = support
	capabilities, err := audit.NewCapabilities(view)
	if err != nil {
		t.Fatal(err)
	}
	return capabilities
}

func allowExactWithBytes(request audit.AccessRequest, maxBytes uint64) (audit.AccessDecision, error) {
	view := request.View()
	grant := audit.AccessGrantSpec{
		Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
		Resources: view.Target.Resources, Actions: view.Query.Actions,
		Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
		Classifications: view.Target.Classifications, Direction: view.Query.Direction,
		ExpiresAt:    time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: maxBytes,
	}
	if view.Query.Scope.Kind == audit.ScopeExact {
		grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
	}
	return audit.AllowAccess(request, grant)
}

func assertExactRevision(t *testing.T, revision audit.RevisionView, reference audit.RevisionRef, values []string) {
	t.Helper()
	if revision.Ref != reference || len(revision.Items) != len(values) {
		t.Fatalf("exact revision = ref %#v, items %d", revision.Ref, len(revision.Items))
	}
	actual := make([]string, len(revision.Items))
	for index, item := range revision.Items {
		if len(item.Values) != 1 || item.Values[0].Knowledge != audit.FieldKnown {
			t.Fatalf("exact revision item %d values = %#v", index, item.Values)
		}
		actual[index] = string(item.Values[0].Canonical)
	}
	if !slices.Equal(actual, values) {
		t.Fatalf("exact revision values = %v, want %v", actual, values)
	}
}

func assertExactItem(t *testing.T, item audit.ItemReadView, ordinal uint16, target, value string) {
	t.Helper()
	if item.Ordinal != ordinal || item.Target.Knowledge != audit.FieldKnown || string(item.Target.Canonical) != target || len(item.Values) != 1 || item.Values[0].Knowledge != audit.FieldKnown || string(item.Values[0].Canonical) != value {
		t.Fatalf("exact item = %#v, want ordinal=%d target=%q value=%q", item, ordinal, target, value)
	}
}

func assertUnsignedExactHistory(
	t *testing.T,
	factory HistoryStoreFactory,
	event *audit.EventType[historyEvent],
	sibling *audit.EventType[historyEvent],
	operation *audit.OperationType,
	semantic audit.SemanticDigester,
	identities audit.IdentityKeyring,
	resolver audit.ContextResolver,
	access audit.ExactAccessQuery,
) {
	t.Helper()
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "history.exact.unsigned", Owner: "history.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, event, sibling, operation)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	change, err := audit.NewCatalogChangeRef("exact.deploy", "unsigned-audittest")
	if err != nil {
		t.Fatal(err)
	}
	clock := &sequenceClock{next: time.Date(2060, 1, 1, 0, 0, 0, 0, time.UTC)}
	opened, err := factory(context.Background(), HistoryStoreRequest{Catalogs: catalogs, Change: change, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	if opened.Close != nil {
		t.Cleanup(func() {
			if !closed {
				if err := opened.Close(); err != nil {
					t.Errorf("close unsigned exact store: %v", err)
				}
			}
		})
	}
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: opened.Writer, Context: resolver,
		Semantics: semantic, Identities: identities, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := event.New(historyEvent{Target: "invoice:unsigned-exact", Value: "unsigned-exact", At: time.Unix(30, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	recordContext := context.WithValue(context.Background(), operationContextKey{}, audit.OperationID{30})
	record, err := recorder.Record(recordContext, draft, audit.InOperation(operation))
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := record.Receipt()
	if !found {
		t.Fatal("unsigned exact record has no receipt")
	}
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: opened.Log,
		Exact: opened.Exact,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			return allowRequested(request)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := history.Revision(context.Background(), audit.RevisionRef{Catalog: catalog.Ref(), Revision: receipt.RevisionID()}, access)
	if err != nil {
		t.Fatal(err)
	}
	assertExactRevision(t, result.Revision(), audit.RevisionRef{Catalog: catalog.Ref(), Revision: receipt.RevisionID()}, []string{"unsigned-exact"})
	if opened.Close == nil {
		t.Fatal("unsigned exact store has no close function")
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if _, err := history.Revision(context.Background(), audit.RevisionRef{Catalog: catalog.Ref(), Revision: receipt.RevisionID()}, access); !errors.Is(err, audit.ErrClosed) {
		t.Fatalf("closed exact store = %v", err)
	}
}
