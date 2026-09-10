package audittest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
)

type HistoryStoreRequest struct {
	Catalogs *audit.CatalogSet
	Change   audit.CatalogChangeRef
	Clock    func() time.Time
}

type HistoryStore struct {
	Writer             audit.Writer
	Log                audit.Log
	Exact              audit.ExactLog
	AmbientTransaction func(context.Context) (context.Context, func() error, error)
	Close              func() error
}

type HistoryStoreFactory func(context.Context, HistoryStoreRequest) (HistoryStore, error)

type historyEvent struct {
	Target string
	Value  string
	At     time.Time
}

type historyEntity struct {
	ID   int64  `db:"id,pk"`
	Name string `db:"name"`
}

type operationContextKey struct{}
type callerValueKey struct{}

type sequenceClock struct {
	mu   sync.Mutex
	next time.Time
}

func (c *sequenceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.next
	c.next = c.next.Add(time.Second)
	return result
}

type historyLogMutation uint8

const (
	historyLogUnchanged historyLogMutation = iota
	historyLogMissingSeal
	historyLogBadSeal
)

type trackingLog struct {
	audit.Log
	mu                     sync.Mutex
	calls                  int
	hostile                bool
	reverse                bool
	forceMultiItemRevision bool
	forcedItemCount        int
	sealMutation           historyLogMutation
}

type replayingHistoryLog struct {
	audit.Log
	mu     sync.Mutex
	stored bool
	page   audit.StoredPage
}

func (l *replayingHistoryLog) Search(ctx context.Context, query audit.StoreQuery) (audit.StoredPage, error) {
	l.mu.Lock()
	if l.stored {
		page := l.page
		l.mu.Unlock()
		return page, nil
	}
	l.mu.Unlock()
	page, err := l.Log.Search(ctx, query)
	if err != nil {
		return page, err
	}
	l.mu.Lock()
	l.stored = true
	l.page = page
	l.mu.Unlock()
	return page, nil
}

func (l *trackingLog) Search(ctx context.Context, query audit.StoreQuery) (audit.StoredPage, error) {
	if ctx.Value(callerValueKey{}) != nil {
		return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("audittest: store received caller value"))
	}
	l.mu.Lock()
	l.calls++
	hostile := l.hostile
	reverse := l.reverse
	forceMultiItemRevision := l.forceMultiItemRevision
	sealMutation := l.sealMutation
	l.mu.Unlock()
	if hostile {
		return audit.StoredPage{}, nil
	}
	page, err := l.Log.Search(ctx, query)
	if err != nil {
		return page, err
	}
	revisions := page.Revisions()
	if reverse {
		slices.Reverse(revisions)
	}
	if forceMultiItemRevision {
		for _, revision := range revisions {
			itemCount := len(revision.View().Revision.Items)
			if itemCount < 2 {
				continue
			}
			revisions = []audit.StoredRevision{revision}
			l.mu.Lock()
			l.forcedItemCount = itemCount
			l.mu.Unlock()
			break
		}
	}
	if sealMutation != historyLogUnchanged {
		for index, revision := range revisions {
			view := revision.View()
			if sealMutation == historyLogMissingSeal {
				view.Revision.Header.Seal = audit.Seal{}
			} else {
				seal := view.Revision.Header.Seal
				view.Revision.Header.Seal, err = audit.NewSeal(seal.Algorithm(), seal.Profile(), seal.KeyID(), bytes.Repeat([]byte{0xff}, 32))
				if err != nil {
					return audit.StoredPage{}, audit.Failure(audit.Corrupt, err)
				}
			}
			revisions[index], err = audit.NewStoredRevision(audit.StoredRevisionData{
				Revision: view.Revision, RecordedAt: view.RecordedAt, Position: view.Position,
				ActiveHoldCount: view.ActiveHoldCount, HoldEpoch: view.HoldEpoch,
				ActiveHoldSet: view.ActiveHoldSet, HoldTransitions: view.HoldTransitions,
			})
			if err != nil {
				return audit.StoredPage{}, audit.Failure(audit.Corrupt, err)
			}
		}
	}
	if !reverse && !forceMultiItemRevision && sealMutation == historyLogUnchanged {
		return page, nil
	}
	var position audit.StorePosition
	var encoded uint64
	for _, revision := range revisions {
		encoded += revision.EncodedBytes()
	}
	if len(revisions) != 0 {
		position = revisions[len(revisions)-1].View().Position
	}
	tampered, err := audit.NewStoredPage(query, audit.StoredPageData{
		Revisions: revisions, Position: position, HasMore: page.HasMore() && !forceMultiItemRevision,
		Progress: audit.SearchProgressView{Pages: 1, Revisions: uint32(len(revisions)), Bytes: encoded},
	})
	if err != nil {
		return audit.StoredPage{}, audit.Failure(audit.Corrupt, err)
	}
	return tampered, nil
}

func (l *trackingLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *trackingLog) forcedItems() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.forcedItemCount
}

func BasicHistory(t *testing.T, factory HistoryStoreFactory) {
	t.Helper()
	if factory == nil {
		t.Fatal("nil history store factory")
	}
	ctx := context.Background()
	policy := audit.ContextFacts(
		audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	unscopedPolicy := audit.ContextFacts(
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	sent := declareHistoryEvent("invoice.sent", policy)
	canceled := declareHistoryEvent("invoice.canceled", policy)
	unscoped := declareHistoryEvent("invoice.unscoped", unscopedPolicy)
	sensitive := audit.Declare(audit.EventPolicy[historyEvent]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("invoice.sensitive", strings.Repeat("06", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: "invoice.sensitive", Owner: "history.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: policy,
		},
		Target:     audit.EventTarget(func(value historyEvent) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsPlaintext),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(historyEvent) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(value historyEvent) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("value", func(value historyEvent) string { return value.Value }, audit.Text(), audit.Internal),
		),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "billing.invoice.process", Semantics: audit.Semantics(1), Retention: "business.forever",
		Consequence: audit.Required, Context: policy, Members: audit.OperationMembers(sent, canceled),
	})
	meta, err := crud.NewMeta[historyEntity]("history_entities")
	if err != nil {
		t.Fatal(err)
	}
	resource := audit.Define(audit.Policy[historyEntity, int64]{
		Model: meta, Semantics: audit.Semantics(1, audit.PolicyGolden("history.entity", strings.Repeat("09", 32))),
		Descriptor: audit.Descriptor{
			Resource: "history.entity", Owner: "history.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: policy,
		},
		Subject: audit.PlaintextSubject(func(id int64) string { return "entity:" + string(rune('0'+id)) }, audit.Public),
		Actions: audit.Actions(audit.EntityCreated),
		Fields:  audit.Fields[historyEntity](audit.Value[historyEntity]("Name", "name", audit.Text(), audit.Public)),
	})
	semantic, err := audit.HMACSemanticDigester("history-semantic", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "history-identity", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := audit.HMACSigner(audit.HMACSigningKey{KeyID: "history-signature", Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "history-signature", Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "history.audit", Owner: "history.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.RequireSignature(signer.Description()),
	}, sent, canceled, sensitive, unscoped, operation, resource)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	change, err := audit.NewCatalogChangeRef("history.deploy", "audittest")
	if err != nil {
		t.Fatal(err)
	}
	clock := &sequenceClock{next: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	opened, err := factory(ctx, HistoryStoreRequest{Catalogs: catalogs, Change: change, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Close != nil {
		t.Cleanup(func() {
			if err := opened.Close(); err != nil {
				t.Errorf("close history store: %v", err)
			}
		})
	}
	if opened.Writer == nil || opened.Log == nil {
		t.Fatal("history store factory returned nil contracts")
	}
	scope, _ := audit.NewContextValue(audit.ScopedReference{Scope: "tenant", Reference: "north"}, audit.Verified)
	resolver := audit.ContextResolverFunc(func(ctx context.Context) (audit.Context, error) {
		operationID, _ := ctx.Value(operationContextKey{}).(audit.OperationID)
		if operationID == (audit.OperationID{}) {
			operationID = audit.OperationID{250}
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
	recordHistoryEvent(t, recorder, operation, sent, historyEvent{Target: "invoice:a", Value: "sent-a", At: time.Unix(1, 0).UTC()}, audit.OperationID{1})
	recordHistoryEvent(t, recorder, operation, canceled, historyEvent{Target: "invoice:a", Value: "canceled-a", At: time.Unix(2, 0).UTC()}, audit.OperationID{2})
	recordHistoryEvent(t, recorder, operation, sent, historyEvent{Target: "invoice:b", Value: "sent-b", At: time.Unix(3, 0).UTC()}, audit.OperationID{3})
	recordStandaloneHistoryEvent(t, recorder, unscoped, historyEvent{Target: "invoice:u", Value: "unscoped-u", At: time.Unix(4, 0).UTC()}, audit.OperationID{4})
	tracked := &trackingLog{Log: opened.Log}
	authorityCalls := 0
	authority := audit.AccessAuthorityFunc(func(ctx context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
		if ctx.Value(callerValueKey{}) != nil {
			return audit.AccessDecision{}, errors.New("audittest: authority received caller value")
		}
		authorityCalls++
		return allowRequested(request)
	})
	if _, err := audit.NewHistory(audit.HistoryConfig{Recorder: recorder, Log: tracked, Access: authority}); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("implicit history profile = %v", err)
	}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked, Access: authority,
	}); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("signed history without verifier = %v", err)
	}
	mismatchedVerifier, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "history-foreign", Key: bytes.Repeat([]byte{4}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked, Access: authority, Verifier: mismatchedVerifier,
	}); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("history with mismatched verifier = %v", err)
	}
	extraVerifier, err := audit.HMACVerifier(
		audit.HMACVerificationKey{KeyID: "history-signature", Key: bytes.Repeat([]byte{3}, 32)},
		audit.HMACVerificationKey{KeyID: "history-extra", Key: bytes.Repeat([]byte{5}, 32)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked, Access: authority, Verifier: extraVerifier,
	}); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("history with extra verifier = %v", err)
	}
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if history.Profile() != audit.PublicOnePageDevelopmentAlpha {
		t.Fatalf("history profile = %v", history.Profile())
	}
	query := audit.Query{
		Purpose: "history.read", Role: "auditor", Scope: audit.CurrentScope(),
		Fields: audit.AllFields(), Context: audit.AllContext(), Direction: audit.OldestFirst, Limit: 10,
	}
	caller := context.WithValue(ctx, callerValueKey{}, "secret")
	missingScope := query
	missingScope.Scope = audit.ScopeSelector{}
	beforeMissingScopeAuthority := authorityCalls
	beforeMissingScopeStore := tracked.count()
	if _, err := sent.History(history).Events(caller, missingScope); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("scoped history without query scope = %v", err)
	}
	if authorityCalls != beforeMissingScopeAuthority || tracked.count() != beforeMissingScopeStore {
		t.Fatal("scoped history without query scope reached authority or store")
	}
	unscopedPage, err := unscoped.History(history).Events(caller, missingScope)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, unscopedPage, []string{"unscoped-u"})
	exactScope, err := audit.ExactScope(audit.ScopedReference{Scope: "tenant", Reference: "north"})
	if err != nil {
		t.Fatal(err)
	}
	exactQuery := query
	exactQuery.Scope = exactScope
	exactPage, err := sent.History(history).Events(caller, exactQuery)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, exactPage, []string{"sent-a", "sent-b"})
	page, err := sent.History(history).Events(caller, query)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, page, []string{"sent-a", "sent-b"})
	if page.HasMore() {
		t.Fatal("complete event page reported truncation")
	}
	metadataOnly := query
	metadataOnly.Fields = audit.NoFields()
	metadataOnly.Context = audit.NoContext()
	metadataPage, err := sent.History(history).Events(caller, metadataOnly)
	if err != nil {
		t.Fatal(err)
	}
	if values := metadataPage.Revisions()[0].Items[0].Values; len(values) != 1 || values[0].Knowledge != audit.FieldUnprojected || len(values[0].Canonical) != 0 || len(metadataPage.Revisions()[0].Context) != 0 {
		t.Fatalf("zero projections disclosed data: %#v", metadataPage.Revisions()[0])
	}
	newest := query
	newest.Direction = audit.NewestFirst
	newest.Limit = 1
	limited, err := operation.History(history).Revisions(caller, newest)
	if err != nil {
		t.Fatal(err)
	}
	if values := historyValues(limited); !slices.Equal(values, []string{"sent-b"}) || !limited.HasMore() {
		t.Fatalf("limited newest page = %v, more=%v", values, limited.HasMore())
	}
	byAction := query
	byAction.Actions = []audit.Action{"invoice.canceled"}
	actionPage, err := operation.History(history).Revisions(caller, byAction)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, actionPage, []string{"canceled-a"})
	operationPage, err := operation.History(history).Operation(caller, audit.OperationID{2}, query)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, operationPage, []string{"canceled-a"})
	targetPage, err := sent.History(history).Target(caller, "invoice:a", query)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, targetPage, []string{"sent-a"})
	resourcePage, err := resource.History(history).Revisions(caller, query)
	if err != nil || len(resourcePage.Revisions()) != 0 {
		t.Fatalf("empty typed resource history = %d, %v", len(resourcePage.Revisions()), err)
	}
	subjectPage, err := resource.History(history).Subject(caller, 1, query)
	if err != nil || len(subjectPage.Revisions()) != 0 {
		t.Fatalf("empty typed subject history = %d, %v", len(subjectPage.Revisions()), err)
	}
	copyOne := page.Revisions()
	copyOne[0].Items[0].Values[0].Canonical[0] = 'X'
	if got := string(page.Revisions()[0].Items[0].Values[0].Canonical); got != "sent-a" {
		t.Fatalf("page read view aliases caller memory: %q", got)
	}
	recordHistoryEvent(t, recorder, operation, sent, historyEvent{Target: "invoice:c", Value: "sent-c", At: time.Unix(4, 0).UTC()}, audit.OperationID{4})
	assertHistoryValues(t, page, []string{"sent-a", "sent-b"})
	recordGroupedHistoryEvents(t, recorder, operation, sent, audit.OperationID{5},
		historyEvent{Target: "invoice:group-a", Value: "group-a", At: time.Unix(5, 0).UTC()},
		historyEvent{Target: "invoice:group-b", Value: "group-b", At: time.Unix(6, 0).UTC()},
	)
	groupTargetPage, err := sent.History(history).Target(caller, "invoice:group-a", query)
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyHistoryItem(t, groupTargetPage, "invoice:group-a", "group-a")
	forcedSiblingLog := &trackingLog{Log: opened.Log, forceMultiItemRevision: true}
	forcedSiblingHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: forcedSiblingLog,
		Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	forcedSiblingPage, err := sent.History(forcedSiblingHistory).Target(caller, "invoice:group-a", query)
	if err != nil {
		t.Fatal(err)
	}
	if forcedSiblingLog.forcedItems() != 2 {
		t.Fatalf("hostile valid store did not return complete sibling revision: items=%d", forcedSiblingLog.forcedItems())
	}
	assertOnlyHistoryItem(t, forcedSiblingPage, "invoice:group-a", "group-a")
	if _, err := page.Cursor(); !errors.Is(err, audit.ErrUnsupported) {
		t.Fatalf("cursor error = %v", err)
	}
	beforeGroup := authorityCalls
	if _, err := recorder.Within(caller, audit.GroupSpec{Operation: operation}, func(groupContext context.Context) error {
		if _, historyErr := sent.History(history).Events(groupContext, query); !errors.Is(historyErr, audit.ErrTransaction) {
			t.Fatalf("history in recorder group = %v", historyErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if authorityCalls != beforeGroup {
		t.Fatal("history in recorder group reached authority")
	}
	if opened.AmbientTransaction != nil {
		transactionContext, settle, transactionErr := opened.AmbientTransaction(caller)
		if transactionErr != nil {
			t.Fatal(transactionErr)
		}
		beforeTransaction := authorityCalls
		if _, historyErr := sent.History(history).Events(transactionContext, query); !errors.Is(historyErr, audit.ErrTransaction) {
			t.Fatalf("history in writer transaction = %v", historyErr)
		}
		if authorityCalls != beforeTransaction {
			t.Fatal("history in writer transaction reached authority")
		}
		if settleErr := settle(); settleErr != nil {
			t.Fatal(settleErr)
		}
	}
	beforeDenial := tracked.count()
	denied, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked,
		Verifier: verifier,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			return audit.DenyAccess(request, "policy.denied")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(denied).Events(caller, query); !errors.Is(err, audit.ErrDenied) || tracked.count() != beforeDenial {
		t.Fatalf("denied history = %v, store calls %d/%d", err, tracked.count(), beforeDenial)
	}
	expanding, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked,
		Verifier: verifier,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			view := request.View()
			grant := audit.AccessGrantSpec{
				Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs, Resources: view.Target.Resources,
				Actions: append(slices.Clone(view.Query.Actions), "invoice.canceled"), Classifications: []audit.Classification{audit.Public},
				Direction: view.Query.Direction, ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
				MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: 1 << 20,
			}
			if view.Query.Scope.Kind == audit.ScopeExact {
				grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
			}
			return audit.AllowAccess(request, grant)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeExpansion := tracked.count()
	if _, err := sent.History(expanding).Events(caller, metadataOnly); !errors.Is(err, audit.ErrDenied) || tracked.count() != beforeExpansion {
		t.Fatalf("expanding grant = %v, store calls %d/%d", err, tracked.count(), beforeExpansion)
	}
	beforeInvalid := authorityCalls
	invalid := query
	invalid.Limit = 0
	if _, err := sent.History(history).Events(caller, invalid); !errors.Is(err, audit.ErrInvalid) || authorityCalls != beforeInvalid {
		t.Fatalf("invalid bound = %v, authority calls %d/%d", err, authorityCalls, beforeInvalid)
	}
	hostileLog := &trackingLog{Log: opened.Log, hostile: true}
	hostile, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: hostileLog, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(hostile).Events(caller, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("hostile store result = %v", err)
	}
	replayedPageLog := &replayingHistoryLog{Log: opened.Log}
	replayedPageHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: replayedPageLog,
		Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(replayedPageHistory).Events(caller, query); err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(replayedPageHistory).Events(caller, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("replayed foreign-origin store page = %v", err)
	}
	var cachedDecision audit.AccessDecision
	cachedDecisionPresent := false
	replayedDecisionHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked,
		Verifier: verifier,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			if cachedDecisionPresent {
				return cachedDecision, nil
			}
			decision, decisionErr := allowRequested(request)
			if decisionErr == nil {
				cachedDecision = decision
				cachedDecisionPresent = true
			}
			return decision, decisionErr
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(replayedDecisionHistory).Events(caller, query); err != nil {
		t.Fatal(err)
	}
	beforeDecisionReplay := tracked.count()
	if _, err := sent.History(replayedDecisionHistory).Events(caller, query); !errors.Is(err, audit.ErrDenied) || tracked.count() != beforeDecisionReplay {
		t.Fatalf("replayed foreign-origin access decision = %v, calls=%d/%d", err, tracked.count(), beforeDecisionReplay)
	}
	missingSealLog := &trackingLog{Log: opened.Log, sealMutation: historyLogMissingSeal}
	missingSealHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: missingSealLog,
		Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(missingSealHistory).Events(caller, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("history with missing record-era seal = %v", err)
	}
	badSealLog := &trackingLog{Log: opened.Log, sealMutation: historyLogBadSeal}
	badSealHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: badSealLog,
		Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(badSealHistory).Events(caller, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("history with substituted record-era seal = %v", err)
	}
	wrongKeyVerifier, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "history-signature", Key: bytes.Repeat([]byte{6}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	wrongKeyHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked,
		Access: authority, Verifier: wrongKeyVerifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(wrongKeyHistory).Events(caller, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("history with wrong verification key = %v", err)
	}
	sensitiveHistory, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked,
		Verifier: verifier,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			view := request.View()
			grant := audit.AccessGrantSpec{
				Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs, Resources: view.Target.Resources,
				Actions: view.Query.Actions, Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
				Classifications: view.Target.Classifications, Direction: view.Query.Direction,
				ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: 1 << 20,
			}
			if view.Query.Scope.Kind == audit.ScopeExact {
				grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
			}
			return audit.AllowAccess(request, grant)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeSensitive := tracked.count()
	if _, err := sensitive.History(sensitiveHistory).Events(caller, query); !errors.Is(err, audit.ErrUnsupported) || tracked.count() != beforeSensitive {
		t.Fatalf("sensitive history without access evidence = %v, store calls %d/%d", err, tracked.count(), beforeSensitive)
	}
	reversedLog := &trackingLog{Log: opened.Log, reverse: true}
	reversed, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: reversedLog, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sent.History(reversed).Events(caller, query); !errors.Is(err, audit.ErrIntegrity) {
		t.Fatalf("reordered store result = %v", err)
	}
	protected := audit.Declare(audit.EventPolicy[historyEvent]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("protected.target", strings.Repeat("07", 32))),
		Descriptor: audit.Descriptor{
			Resource: "protected.event", Action: "protected.seen", Owner: "history.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: policy,
		},
		Target:     audit.EventTarget(func(value historyEvent) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsProtected),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(historyEvent) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(value historyEvent) time.Time { return value.At }),
	})
	if _, err := protected.TargetRef("protected:a"); !errors.Is(err, audit.ErrUnsupported) {
		t.Fatalf("protected target ref = %v", err)
	}
	assertUnsignedHistoryAlpha(t, factory, semantic, identities, unscoped, resolver)
}

func declareHistoryEvent(action audit.Action, policy audit.ContextPolicy) *audit.EventType[historyEvent] {
	return audit.Declare(audit.EventPolicy[historyEvent]{
		Semantics: audit.Semantics(1, audit.PolicyGolden(audit.FixtureName(action), strings.Repeat("08", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: action, Owner: "history.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: policy,
		},
		Target:     audit.EventTarget(func(value historyEvent) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsPlaintext),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(historyEvent) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(value historyEvent) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("value", func(value historyEvent) string { return value.Value }, audit.Text(), audit.Public),
		),
	})
}

func recordHistoryEvent(t *testing.T, recorder *audit.Recorder, operation *audit.OperationType, event *audit.EventType[historyEvent], value historyEvent, operationID audit.OperationID) {
	t.Helper()
	draft, err := event.New(value)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), operationContextKey{}, operationID)
	if _, err := recorder.Record(ctx, draft, audit.InOperation(operation)); err != nil {
		t.Fatal(err)
	}
}

func recordStandaloneHistoryEvent(t *testing.T, recorder *audit.Recorder, event *audit.EventType[historyEvent], value historyEvent, operationID audit.OperationID) {
	t.Helper()
	draft, err := event.New(value)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), operationContextKey{}, operationID)
	if _, err := recorder.Record(ctx, draft); err != nil {
		t.Fatal(err)
	}
}

func recordGroupedHistoryEvents(
	t *testing.T,
	recorder *audit.Recorder,
	operation *audit.OperationType,
	event *audit.EventType[historyEvent],
	operationID audit.OperationID,
	values ...historyEvent,
) audit.RevisionID {
	t.Helper()
	ctx := context.WithValue(context.Background(), operationContextKey{}, operationID)
	result, err := recorder.Within(ctx, audit.GroupSpec{Operation: operation}, func(groupContext context.Context) error {
		for _, value := range values {
			draft, draftErr := event.New(value)
			if draftErr != nil {
				return draftErr
			}
			if stageErr := recorder.Stage(groupContext, draft); stageErr != nil {
				return stageErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := result.Receipt()
	if !found {
		t.Fatal("grouped history result has no receipt")
	}
	return receipt.RevisionID()
}

func assertUnsignedHistoryAlpha(
	t *testing.T,
	factory HistoryStoreFactory,
	semantic audit.SemanticDigester,
	identities audit.IdentityKeyring,
	event *audit.EventType[historyEvent],
	resolver audit.ContextResolver,
) {
	t.Helper()
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "history.unsigned", Owner: "history.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, event)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	change, err := audit.NewCatalogChangeRef("history.deploy", "unsigned-audittest")
	if err != nil {
		t.Fatal(err)
	}
	clock := &sequenceClock{next: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)}
	opened, err := factory(context.Background(), HistoryStoreRequest{Catalogs: catalogs, Change: change, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Close != nil {
		t.Cleanup(func() {
			if err := opened.Close(); err != nil {
				t.Errorf("close unsigned history store: %v", err)
			}
		})
	}
	if opened.Writer == nil || opened.Log == nil {
		t.Fatal("unsigned history store factory returned nil contracts")
	}
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: opened.Writer, Context: resolver,
		Semantics: semantic, Identities: identities, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	recordStandaloneHistoryEvent(t, recorder, event, historyEvent{
		Target: "invoice:unsigned", Value: "unsigned", At: time.Unix(10, 0).UTC(),
	}, audit.OperationID{10})
	tracked := &trackingLog{Log: opened.Log}
	authority := audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
		return allowRequested(request)
	})
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked, Access: authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := event.History(history).Events(context.Background(), audit.Query{
		Purpose: "history.read", Role: "auditor", Fields: audit.AllFields(), Context: audit.AllContext(),
		Direction: audit.OldestFirst, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryValues(t, page, []string{"unsigned"})
	extra, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "history-extra", Key: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: tracked, Access: authority, Verifier: extra,
	}); !errors.Is(err, audit.ErrDeclaration) {
		t.Fatalf("unsigned history with extra verifier = %v", err)
	}
}

func allowRequested(request audit.AccessRequest) (audit.AccessDecision, error) {
	view := request.View()
	grant := audit.AccessGrantSpec{
		Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
		Resources: view.Target.Resources, Actions: view.Query.Actions,
		Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
		Classifications: []audit.Classification{audit.Public}, Direction: view.Query.Direction,
		ExpiresAt:    time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: 1 << 20,
	}
	if view.Query.Scope.Kind == audit.ScopeExact {
		grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
	}
	return audit.AllowAccess(request, grant)
}

func assertHistoryValues(t *testing.T, page audit.Page, want []string) {
	t.Helper()
	if got := historyValues(page); !slices.Equal(got, want) {
		t.Fatalf("history values = %v, want %v", got, want)
	}
}

func assertOnlyHistoryItem(t *testing.T, page audit.Page, target, value string) {
	t.Helper()
	revisions := page.Revisions()
	if len(revisions) != 1 || len(revisions[0].Items) != 1 {
		t.Fatalf("projected history shape = %d revisions, %d items", len(revisions), historyItemCount(revisions))
	}
	item := revisions[0].Items[0]
	if item.Target.Knowledge != audit.FieldKnown || string(item.Target.Canonical) != target {
		t.Fatalf("projected history target = %#v, want %q", item.Target, target)
	}
	if len(item.Values) != 1 || item.Values[0].Knowledge != audit.FieldKnown || string(item.Values[0].Canonical) != value {
		t.Fatalf("projected history values = %#v, want %q", item.Values, value)
	}
}

func historyItemCount(revisions []audit.RevisionView) int {
	count := 0
	for _, revision := range revisions {
		count += len(revision.Items)
	}
	return count
}

func historyValues(page audit.Page) []string {
	revisions := page.Revisions()
	result := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		if len(revision.Items) == 0 || len(revision.Items[0].Values) == 0 {
			result = append(result, "")
			continue
		}
		result = append(result, string(revision.Items[0].Values[0].Canonical))
	}
	return result
}
