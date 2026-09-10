package auditcrud

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
)

type alphaRow struct {
	ID        int64      `db:"id,pk"`
	Name      string     `db:"name"`
	DeletedAt *time.Time `db:"-"`
}

type alphaPatch struct {
	Name string `db:"name"`
}

type alphaDatabase struct {
	mu          sync.Mutex
	rows        map[int64]alphaRow
	nextID      int64
	writer      *alphaWriter
	commitErr   error
	rollbackErr error
	beginCalls  int
	commits     int
	rollbacks   int
}

type alphaSource struct {
	database *alphaDatabase
}

type alphaTx struct {
	mu      sync.Mutex
	source  *alphaSource
	rows    map[int64]alphaRow
	nextID  int64
	pending []alphaPendingAppend
	state   uint8
}

type alphaCore struct {
	meta   *crud.Meta
	source *alphaSource

	mu         sync.Mutex
	readCalls  int
	writeCalls int
	panicSave  bool
	unboundTx  bool
}

type alphaHead struct {
	previous audit.LeafDigest
	terminal bool
}

type alphaPendingAppend struct {
	request audit.AppendRequest
	stored  audit.StoredHeader
}

type alphaWriter struct {
	mu        sync.Mutex
	source    *alphaSource
	state     audit.StoreCatalogState
	backing   audit.Backing
	backingID audit.BackingID
	logID     audit.LogID
	limits    audit.Limits
	caps      audit.Capabilities
	sequence  uint64
	failNext  error
	revisions []audit.RevisionWireView
	stored    map[audit.RevisionID]audit.StoredHeader
	heads     map[audit.EntityChainID]alphaHead
}

type alphaExecution struct {
	writer    *alphaWriter
	tx        *alphaTx
	authority audit.Authority
}

type alphaFixture struct {
	core       *alphaCore
	secured    crud.Core[alphaRow, int64]
	middleware crud.Middleware[alphaRow, int64]
	resource   *audit.ResourcePolicy[alphaRow, int64]
	operation  *audit.OperationType
	recorder   *audit.Recorder
	writer     *alphaWriter
	authMu     sync.Mutex
	authorized []security.Action
}

var _ crud.Core[alphaRow, int64] = (*terminal[alphaRow, int64])(nil)
var _ crud.Core[alphaRow, int64] = (*alphaCore)(nil)
var _ crud.Source = (*alphaSource)(nil)
var _ crud.Beginner = (*alphaSource)(nil)
var _ crud.Tx = (*alphaTx)(nil)
var _ audit.Writer = (*alphaWriter)(nil)
var _ audit.Execution = (*alphaExecution)(nil)

func TestSecuredIsASealedCoreWithoutAnUnwrapPath(t *testing.T) {
	fixture := newAlphaFixture(t)
	var core crud.Core[alphaRow, int64] = fixture.secured
	if core == nil {
		t.Fatal("Secured returned a nil CRUD core")
	}
	if _, ok := fixture.secured.(interface {
		Next() crud.Core[alphaRow, int64]
	}); ok {
		t.Fatal("Secured exposed Next through its mutation boundary")
	}
	if _, ok := reflect.TypeOf(fixture.secured).MethodByName("Next"); ok {
		t.Fatal("Secured concrete method set exposed Next")
	}
	if _, ok := fixture.secured.(interface{ MutationBoundarySealed() }); !ok {
		t.Fatal("Secured did not mark its terminal mutation boundary")
	}
	if source, ok := crud.SourceOf(fixture.secured); !ok || source != fixture.core.source {
		t.Fatal("Secured did not expose the exact validated transaction source")
	}
}

func TestSecuredBindsExactResourceMetadataAndAuthorizesReads(t *testing.T) {
	fixture := newAlphaFixture(t)
	fixture.core.seed(alphaRow{ID: 41, Name: "visible"})

	row, err := fixture.secured.GetByID(context.Background(), 41)
	if err != nil {
		t.Fatalf("read through Secured: %v", err)
	}
	if row.ID != 41 || row.Name != "visible" || fixture.core.readCount() != 1 {
		t.Fatalf("read result = %+v, underlying reads = %d", row, fixture.core.readCount())
	}
	if got := fixture.actions(); !slices.Equal(got, []security.Action{security.Read}) {
		t.Fatalf("authorized actions = %v, want one read", got)
	}

	wrong, err := crud.NewMeta[alphaRow]("auditcrud_other_rows")
	if err != nil {
		t.Fatal(err)
	}
	bad := &alphaCore{meta: wrong, source: fixture.core.source}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = fixture.middleware(bad)
	}()
	if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "resource metadata") {
		t.Fatalf("wrong metadata bind panic = %v, want resource metadata refusal", recovered)
	}
}

func TestSecuredCommitsCreateUpdateAndHardDeleteWithTheirAuditRevision(t *testing.T) {
	fixture := newAlphaFixture(t)
	ctx := context.Background()

	created, err := fixture.secured.Save(ctx, &alphaRow{Name: "draft"})
	if err != nil {
		t.Fatalf("create through Secured: %v", err)
	}
	if created.ID == 0 || created.Name != "draft" {
		t.Fatalf("created row = %+v", created)
	}
	assertAlphaCommittedState(t, fixture, map[int64]string{created.ID: "draft"}, audit.EntityCreated)

	updated, err := fixture.secured.Update(ctx, created.ID, alphaPatch{Name: "published"})
	if err != nil {
		t.Fatalf("update through Secured: %v", err)
	}
	if updated.ID != created.ID || updated.Name != "published" {
		t.Fatalf("updated row = %+v", updated)
	}
	assertAlphaCommittedState(t, fixture, map[int64]string{created.ID: "published"}, audit.EntityCreated, audit.EntityChanged)

	deleted, err := fixture.secured.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("hard delete through Secured: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("hard delete count = %d, want 1", deleted)
	}
	assertAlphaCommittedState(t, fixture, nil, audit.EntityCreated, audit.EntityChanged, audit.EntityHardDeleted)

	begin, commit, rollback := fixture.core.settlements()
	if begin != 3 || commit != 3 || rollback != 0 {
		t.Fatalf("transactions = begin:%d commit:%d rollback:%d, want 3/3/0", begin, commit, rollback)
	}
	if got := fixture.actions(); !slices.Equal(got, []security.Action{security.Create, security.Update, security.Delete}) {
		t.Fatalf("authorized actions = %v, want create/update/delete", got)
	}
}

func TestSecuredClassifiesAssignedSaveAndCapturesAFirstTouchBaseline(t *testing.T) {
	fixture := newAlphaFixture(t)
	fixture.core.seed(alphaRow{ID: 41, Name: "legacy"})

	updated, err := fixture.secured.Save(context.Background(), &alphaRow{ID: 41, Name: "adopted"})
	if err != nil || updated.Name != "adopted" {
		t.Fatalf("assigned existing save = (%+v, %v)", updated, err)
	}
	revisions := fixture.writer.committedRevisions()
	if len(revisions) != 1 || len(revisions[0].Items) != 1 || revisions[0].Items[0].Action != audit.Action(audit.EntityChanged) || revisions[0].Items[0].EntityState != audit.EntityFullState {
		t.Fatalf("first-touch revision = %+v", revisions)
	}

	created, err := fixture.secured.Save(context.Background(), &alphaRow{ID: 42, Name: "assigned"})
	if err != nil || created.ID != 42 {
		t.Fatalf("assigned create = (%+v, %v)", created, err)
	}
	revisions = fixture.writer.committedRevisions()
	if len(revisions) != 2 || revisions[1].Items[0].Action != audit.Action(audit.EntityCreated) {
		t.Fatalf("assigned create revisions = %+v", revisions)
	}
	if got := fixture.actions(); !slices.Equal(got, []security.Action{security.Create, security.Update, security.Create, security.Update}) {
		t.Fatalf("assigned save authorization = %v", got)
	}
}

func TestSecuredRollsBackBusinessCreateWhenAuditAppendFails(t *testing.T) {
	fixture := newAlphaFixture(t)
	fixture.writer.fail(errors.New("audit unavailable"))

	_, err := fixture.secured.Save(context.Background(), &alphaRow{Name: "must rollback"})
	if !errors.Is(err, audit.ErrNotWritten) {
		t.Fatalf("create error = %v, want audit.ErrNotWritten", err)
	}
	if rows := fixture.core.committedRows(); len(rows) != 0 {
		t.Fatalf("business rows survived failed audit append: %+v", rows)
	}
	if revisions := fixture.writer.committedRevisions(); len(revisions) != 0 {
		t.Fatalf("audit revisions survived failed append: %d", len(revisions))
	}
	if _, ok := audit.RetryTokenOf(err); ok {
		t.Fatal("transactional audit failure exposed a standalone retry token")
	}
	begin, commit, rollback := fixture.core.settlements()
	if begin != 1 || commit != 0 || rollback != 1 {
		t.Fatalf("transactions = begin:%d commit:%d rollback:%d, want 1/0/1", begin, commit, rollback)
	}
}

func TestSecuredClassifiesAnUnconfirmedOwnedTransactionOutcome(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		fixture := newAlphaFixture(t)
		backend := errors.New("driver commit detail")
		fixture.core.source.database.commitErr = backend

		_, err := fixture.secured.Save(context.Background(), &alphaRow{Name: "possibly committed"})
		if !errors.Is(err, audit.ErrCommitUnconfirmed) || errors.Is(err, backend) {
			t.Fatalf("commit outcome error = %v", err)
		}
		if err.Error() != audit.ErrCommitUnconfirmed.Error() {
			t.Fatalf("commit outcome exposed backend detail: %q", err)
		}
		key, ok := audit.ReconcileKeyOf(err)
		if !ok {
			t.Fatal("commit uncertainty lost its reconcile key")
		}
		if len(fixture.core.committedRows()) != 1 || len(fixture.writer.committedRevisions()) != 1 {
			t.Fatal("commit uncertainty fixture did not publish both atomic states")
		}
		lookup, lookupErr := fixture.recorder.Lookup(context.Background(), key)
		if lookupErr != nil || lookup.State() != audit.Found {
			t.Fatalf("committed uncertain revision lookup = (%v, %v)", lookup.State(), lookupErr)
		}
	})

	t.Run("rollback", func(t *testing.T) {
		fixture := newAlphaFixture(t)
		fixture.writer.fail(errors.New("audit unavailable"))
		fixture.core.source.database.rollbackErr = errors.New("driver rollback detail")

		_, err := fixture.secured.Save(context.Background(), &alphaRow{Name: "must not escape"})
		if !errors.Is(err, audit.ErrRollbackUnconfirmed) || !errors.Is(err, audit.ErrNotWritten) {
			t.Fatalf("rollback outcome error = %v", err)
		}
		if err.Error() != audit.ErrRollbackUnconfirmed.Error() {
			t.Fatalf("rollback outcome exposed backend detail: %q", err)
		}
		key, ok := audit.ReconcileKeyOf(err)
		if !ok {
			t.Fatal("rollback uncertainty lost its reconcile key")
		}
		if len(fixture.core.committedRows()) != 0 || len(fixture.writer.committedRevisions()) != 0 {
			t.Fatal("rollback uncertainty fixture published business or audit state")
		}
		lookup, lookupErr := fixture.recorder.Lookup(context.Background(), key)
		if lookupErr != nil || lookup.State() != audit.AbsentNow {
			t.Fatalf("rolled-back uncertain revision lookup = (%v, %v)", lookup.State(), lookupErr)
		}
		if _, ok := audit.RetryTokenOf(err); ok {
			t.Fatal("rollback uncertainty exposed a standalone retry token")
		}
	})
}

func TestSecuredDrivesTheValidatedSourceInsteadOfTheInnerCoreTransaction(t *testing.T) {
	fixture := newAlphaFixture(t)
	fixture.core.mu.Lock()
	fixture.core.unboundTx = true
	fixture.core.mu.Unlock()

	created, err := fixture.secured.Save(context.Background(), &alphaRow{Name: "source-owned"})
	if err != nil {
		t.Fatal(err)
	}
	assertAlphaCommittedState(t, fixture, map[int64]string{created.ID: "source-owned"}, audit.EntityCreated)
}

func TestSecuredRefusesUnsupportedMutationsBeforeAuthorizationOrStorage(t *testing.T) {
	fixture := newAlphaFixture(t)
	ctx := context.Background()
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "save only", run: func() error { return fixture.secured.SaveOnly(ctx, &alphaRow{Name: "write only"}) }},
		{name: "update all", run: func() error {
			_, err := fixture.secured.UpdateAll(ctx, alphaPatch{Name: "bulk"})
			return err
		}},
		{name: "save all", run: func() error { return fixture.secured.SaveAll(ctx, []*alphaRow{{Name: "bulk"}}) }},
		{name: "delete all", run: func() error {
			_, err := fixture.secured.DeleteAll(ctx)
			return err
		}},
	}
	for _, test := range tests {
		if err := test.run(); !errors.Is(err, audit.ErrUnsupported) {
			t.Fatalf("%s error = %v, want audit.ErrUnsupported", test.name, err)
		}
	}
	if got := fixture.actions(); len(got) != 0 {
		t.Fatalf("unsupported mutations invoked authorization: %v", got)
	}
	if begin, commit, rollback := fixture.core.settlements(); begin != 0 || commit != 0 || rollback != 0 {
		t.Fatalf("unsupported mutation transactions = begin:%d commit:%d rollback:%d", begin, commit, rollback)
	}
	if revisions := fixture.writer.committedRevisions(); len(revisions) != 0 {
		t.Fatalf("unsupported mutations wrote %d audit revisions", len(revisions))
	}
}

func TestSecuredEmptyDeleteAuthorizesButDoesNotOpenAWriteTransaction(t *testing.T) {
	fixture := newAlphaFixture(t)
	count, err := fixture.secured.Delete(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("empty delete = (%d, %v), want (0, nil)", count, err)
	}
	if got := fixture.actions(); !slices.Equal(got, []security.Action{security.Delete}) {
		t.Fatalf("empty delete authorization = %v, want delete", got)
	}
	if begin, commit, rollback := fixture.core.settlements(); begin != 0 || commit != 0 || rollback != 0 {
		t.Fatalf("empty delete transactions = begin:%d commit:%d rollback:%d", begin, commit, rollback)
	}
	if revisions := fixture.writer.committedRevisions(); len(revisions) != 0 {
		t.Fatalf("empty delete wrote %d audit revisions", len(revisions))
	}
}

func TestSecuredRequiresAnAuditGroupInsideACallerOwnedTransaction(t *testing.T) {
	t.Run("without group", func(t *testing.T) {
		fixture := newAlphaFixture(t)
		err := fixture.secured.Tx(context.Background(), func(transactionContext context.Context) error {
			_, saveErr := fixture.secured.Save(transactionContext, &alphaRow{Name: "refused"})
			return saveErr
		})
		if !errors.Is(err, audit.ErrTransaction) {
			t.Fatalf("caller-owned transaction without group error = %v, want audit.ErrTransaction", err)
		}
		if fixture.core.writeCount() != 0 {
			t.Fatalf("caller-owned transaction reached %d business mutations before group refusal", fixture.core.writeCount())
		}
		if len(fixture.core.committedRows()) != 0 || len(fixture.writer.committedRevisions()) != 0 {
			t.Fatal("refused caller-owned transaction published business or audit state")
		}
		if begin, commit, rollback := fixture.core.settlements(); begin != 1 || commit != 0 || rollback != 1 {
			t.Fatalf("refused transaction = begin:%d commit:%d rollback:%d, want 1/0/1", begin, commit, rollback)
		}
	})

	t.Run("with group", func(t *testing.T) {
		fixture := newAlphaFixture(t)
		var created alphaRow
		var group audit.GroupResult
		err := fixture.secured.Tx(context.Background(), func(transactionContext context.Context) error {
			var groupErr error
			group, groupErr = fixture.recorder.Within(transactionContext, audit.GroupSpec{Operation: fixture.operation}, func(groupContext context.Context) error {
				var saveErr error
				created, saveErr = fixture.secured.Save(groupContext, &alphaRow{Name: "grouped"})
				return saveErr
			})
			if groupErr != nil {
				return groupErr
			}
			if len(fixture.core.committedRows()) != 0 || len(fixture.writer.committedRevisions()) != 0 {
				t.Fatal("caller-owned transaction became visible before commit")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("caller-owned transaction with group: %v", err)
		}
		receipt, ok := group.Receipt()
		if !ok || receipt.Settlement() != audit.InCallerTransaction {
			t.Fatalf("group receipt = %#v, %v, want InCallerTransaction", receipt, ok)
		}
		if fixture.core.writeCount() != 1 {
			t.Fatalf("grouped transaction business mutations = %d, want 1", fixture.core.writeCount())
		}
		assertAlphaCommittedState(t, fixture, map[int64]string{created.ID: "grouped"}, audit.EntityCreated)
		if begin, commit, rollback := fixture.core.settlements(); begin != 1 || commit != 1 || rollback != 0 {
			t.Fatalf("grouped transaction = begin:%d commit:%d rollback:%d, want 1/1/0", begin, commit, rollback)
		}
	})
}

func TestSecuredPanicPoisonsARecoveredNestedGroup(t *testing.T) {
	fixture := newAlphaFixture(t)
	fixture.core.mu.Lock()
	fixture.core.panicSave = true
	fixture.core.mu.Unlock()
	err := fixture.secured.Tx(context.Background(), func(transactionContext context.Context) error {
		_, groupErr := fixture.recorder.Within(transactionContext, audit.GroupSpec{Operation: fixture.operation}, func(groupContext context.Context) error {
			func() {
				defer func() { _ = recover() }()
				_, _ = fixture.secured.Save(groupContext, &alphaRow{Name: "must rollback"})
			}()
			return nil
		})
		return groupErr
	})
	if !errors.Is(err, audit.ErrGroupPoisoned) {
		t.Fatalf("recovered nested panic error = %v, want audit.ErrGroupPoisoned", err)
	}
	if len(fixture.core.committedRows()) != 0 || len(fixture.writer.committedRevisions()) != 0 {
		t.Fatal("recovered nested panic published business or audit state")
	}
	if begin, commit, rollback := fixture.core.settlements(); begin != 1 || commit != 0 || rollback != 1 {
		t.Fatalf("recovered panic transaction = begin:%d commit:%d rollback:%d, want 1/0/1", begin, commit, rollback)
	}
}

func TestSecuredCannotReuseAGroupContextAfterItsOwnerPanics(t *testing.T) {
	fixture := newAlphaFixture(t)
	panicValue := errors.New("owner panic")
	var leaked context.Context
	var recovered any
	err := fixture.secured.Tx(context.Background(), func(transactionContext context.Context) error {
		func() {
			defer func() { recovered = recover() }()
			_, _ = fixture.recorder.Within(transactionContext, audit.GroupSpec{Operation: fixture.operation}, func(groupContext context.Context) error {
				leaked = groupContext
				panic(panicValue)
			})
		}()
		if recovered != panicValue || leaked == nil {
			t.Fatalf("owner panic = %v, leaked context = %v", recovered, leaked != nil)
		}
		_, saveErr := fixture.secured.Save(leaked, &alphaRow{Name: "must be refused"})
		return saveErr
	})
	if !errors.Is(err, audit.ErrGroupClosed) {
		t.Fatalf("reuse after owner panic error = %v", err)
	}
	if fixture.core.writeCount() != 0 || len(fixture.core.committedRows()) != 0 || len(fixture.writer.committedRevisions()) != 0 {
		t.Fatal("closed owner group allowed a business or audit write")
	}
}

func assertAlphaCommittedState(t *testing.T, fixture *alphaFixture, rows map[int64]string, actions ...audit.EntityAction) {
	t.Helper()
	committed := fixture.core.committedRows()
	if len(committed) != len(rows) {
		t.Fatalf("committed business row count = %d, want %d", len(committed), len(rows))
	}
	for id, name := range rows {
		if got, ok := committed[id]; !ok || got.Name != name {
			t.Fatalf("committed business row %d = %+v, want name %q", id, got, name)
		}
	}
	revisions := fixture.writer.committedRevisions()
	if len(revisions) != len(actions) {
		t.Fatalf("committed audit revision count = %d, want %d", len(revisions), len(actions))
	}
	for index, action := range actions {
		if len(revisions[index].Items) != 1 || revisions[index].Items[0].Kind != audit.EntityItem || revisions[index].Items[0].Action != audit.Action(action) {
			t.Fatalf("audit revision %d items = %+v, want one %s entity item", index, revisions[index].Items, action)
		}
	}
}

func newAlphaFixture(t *testing.T) *alphaFixture {
	t.Helper()
	meta, err := crud.NewMeta[alphaRow]("auditcrud_alpha_rows")
	if err != nil {
		t.Fatal(err)
	}
	operationContext := audit.ContextFacts(audit.GeneratedOperationFact(audit.Internal, audit.AsPlaintext))
	resource := audit.Define(audit.Policy[alphaRow, int64]{
		Model:     meta,
		Semantics: audit.Semantics(1, audit.PolicyGolden("alpha.resource", strings.Repeat("01", 32))),
		Descriptor: audit.Descriptor{
			Resource: "alpha.row", Owner: "alpha.team", Purpose: "business.audit",
			Retention: "business.forever", Consequence: audit.Required, Context: operationContext,
		},
		Subject: audit.PlaintextSubject(func(id int64) string { return fmt.Sprintf("row:%d", id) }, audit.Internal),
		Actions: audit.Actions(audit.EntityCreated, audit.EntityChanged, audit.EntityHardDeleted),
		Fields: audit.Fields[alphaRow](
			audit.Value[alphaRow]("Name", "name", audit.Text(), audit.Internal),
		),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "alpha.row.mutate", Semantics: audit.Semantics(1),
		Retention: "business.forever", Consequence: audit.Required, Context: operationContext,
		Members: audit.OperationMembers(
			resource.Action(audit.EntityCreated),
			resource.Action(audit.EntityChanged),
			resource.Action(audit.EntityHardDeleted),
		),
	})
	semantic, err := audit.HMACSemanticDigester("semantic-alpha", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "identity-alpha", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "alpha.audit", Owner: "alpha.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, resource, operation)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	database := &alphaDatabase{rows: make(map[int64]alphaRow), nextID: 1}
	source := &alphaSource{database: database}
	writer := newAlphaWriter(t, source, catalogs)
	database.writer = writer
	resolver, err := audit.StaticContext(audit.Context{})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: writer, Context: resolver,
		Semantics: semantic, Identities: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	core := &alphaCore{meta: meta, source: source}
	fixture := &alphaFixture{core: core, resource: resource, operation: operation, recorder: recorder, writer: writer}
	policy := security.Policy[alphaRow, int64]{Authorize: func(_ context.Context, action security.Action) error {
		fixture.authMu.Lock()
		fixture.authorized = append(fixture.authorized, action)
		fixture.authMu.Unlock()
		return nil
	}}
	fixture.middleware = Secured(recorder, resource, policy)
	fixture.secured = fixture.middleware(core)
	return fixture
}

func newAlphaWriter(t *testing.T, source *alphaSource, catalogs *audit.CatalogSet) *alphaWriter {
	t.Helper()
	state, err := audit.NewStoreCatalogState(catalogs.Active(), catalogs.Digest())
	if err != nil {
		t.Fatal(err)
	}
	backing, err := audit.BackingFor(source.database)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := audit.NewCapabilities(audit.CapabilitySpec{
		Transactions: audit.SupportSupported, CrossSystemAtomic: audit.SupportSupported,
		Persistence: audit.SupportUnsupported, Idempotency: audit.SupportSupported,
		Reconciliation: audit.SupportSupported, StableSearch: audit.SupportUnsupported,
		ExactInspection: audit.SupportUnsupported, AttemptLifecycle: audit.SupportUnsupported,
		Holds: audit.SupportUnsupported, PurgePlanning: audit.SupportUnsupported,
	})
	if err != nil {
		t.Fatal(err)
	}
	limits, err := audit.NewLimits(alphaLimitSpec())
	if err != nil {
		t.Fatal(err)
	}
	writer := &alphaWriter{
		source: source, state: state, backing: backing, limits: limits, caps: capabilities,
		heads: make(map[audit.EntityChainID]alphaHead), stored: make(map[audit.RevisionID]audit.StoredHeader),
	}
	writer.backingID[0] = 1
	writer.logID[0] = 2
	return writer
}

func alphaLimitSpec() audit.LimitSpec {
	return audit.LimitSpec{
		RevisionBytes: 1 << 20, AppendRequestBytes: 2 << 20,
		PageRevisions: 100, PageBytes: 2 << 20, ExactTargets: 100, ExactBytes: 2 << 20,
		PositionBytes: 64, InventoryCandidates: 100, InventoryCohorts: 100, InventoryBytes: 2 << 20,
		SnapshotBytes: 1 << 20, SearchCohortRevisions: 1000, SearchCohortBytes: 4 << 20,
		AttemptTransitions: 32, AttemptStateBytes: 1 << 20, AttemptOpenLifetime: 24 * time.Hour,
	}
}

func (fixture *alphaFixture) actions() []security.Action {
	fixture.authMu.Lock()
	defer fixture.authMu.Unlock()
	return slices.Clone(fixture.authorized)
}

func (database *alphaDatabase) snapshot() (map[int64]alphaRow, int64) {
	database.mu.Lock()
	defer database.mu.Unlock()
	return cloneAlphaRows(database.rows), database.nextID
}

func (source *alphaSource) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, errors.New("alpha source does not execute statements")
}

func (source *alphaSource) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, errors.New("alpha source does not execute queries")
}

func (*alphaSource) Dialect() crud.Dialect { return crud.Postgres{} }

func (source *alphaSource) DataSource() any { return source.database }

func (source *alphaSource) Begin(ctx context.Context) (crud.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, nextID := source.database.snapshot()
	source.database.mu.Lock()
	source.database.beginCalls++
	source.database.mu.Unlock()
	return &alphaTx{source: source, rows: rows, nextID: nextID}, nil
}

func (tx *alphaTx) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, errors.New("alpha transaction does not execute statements")
}

func (tx *alphaTx) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, errors.New("alpha transaction does not execute queries")
}

func (tx *alphaTx) DataSource() any { return tx.source.database }

func (tx *alphaTx) InTransaction() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.state == 0
}

func (tx *alphaTx) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != 0 {
		return errors.New("alpha transaction is settled")
	}
	database := tx.source.database
	writer := database.writer
	database.mu.Lock()
	writer.mu.Lock()
	database.rows = cloneAlphaRows(tx.rows)
	database.nextID = tx.nextID
	for _, pending := range tx.pending {
		view := pending.request.View().Revision.View()
		writer.revisions = append(writer.revisions, view)
		writer.stored[view.Header.RevisionID] = pending.stored
		for _, item := range view.Items {
			if item.Kind == audit.EntityItem {
				writer.heads[item.Chain] = alphaHead{previous: item.Leaf, terminal: item.Action == audit.Action(audit.EntityHardDeleted)}
			}
		}
	}
	writer.mu.Unlock()
	commitErr := database.commitErr
	if commitErr == nil {
		database.commits++
	}
	database.mu.Unlock()
	tx.state = 1
	return commitErr
}

func (tx *alphaTx) Rollback(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != 0 {
		return errors.New("alpha transaction is settled")
	}
	tx.state = 2
	tx.pending = nil
	tx.source.database.mu.Lock()
	err := tx.source.database.rollbackErr
	tx.source.database.rollbacks++
	tx.source.database.mu.Unlock()
	return err
}

func (core *alphaCore) Meta() *crud.Meta { return core.meta }

func (core *alphaCore) Source() crud.Source { return core.source }

func (core *alphaCore) GetByID(ctx context.Context, id int64, _ ...crud.Option) (alphaRow, error) {
	core.mu.Lock()
	core.readCalls++
	core.mu.Unlock()
	rows, err := core.rows(ctx)
	if err != nil {
		return alphaRow{}, err
	}
	row, ok := rows[id]
	if !ok {
		return alphaRow{}, crud.ErrNotFound
	}
	return row, nil
}

func (core *alphaCore) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[alphaRow], error) {
	rows, err := core.GetAll(ctx, options...)
	if err != nil {
		return crud.PaginatedResponse[alphaRow]{}, err
	}
	return crud.NewPaginatedResponse(rows, 1, len(rows), int64(len(rows))), nil
}

func (core *alphaCore) GetAll(ctx context.Context, options ...crud.Option) ([]alphaRow, error) {
	rows, err := core.rows(ctx)
	if err != nil {
		return nil, err
	}
	resolved, err := crud.ReadOptions.Build(core.meta.Name, options...)
	if err != nil {
		return nil, err
	}
	filterSQL, arguments, err := crud.NewSQL(crud.Postgres{}, core.meta).Predicate(resolved.Predicate()).Done()
	if err != nil {
		return nil, err
	}
	if strings.Contains(filterSQL, `"id"`) {
		allowed := make(map[int64]struct{})
		for _, argument := range arguments {
			if id, ok := argument.(int64); ok {
				allowed[id] = struct{}{}
			}
		}
		for id := range rows {
			if _, ok := allowed[id]; !ok {
				delete(rows, id)
			}
		}
	}
	ids := make([]int64, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	result := make([]alphaRow, 0, len(ids))
	for _, id := range ids {
		result = append(result, rows[id])
	}
	return result, nil
}

func (core *alphaCore) First(ctx context.Context, options ...crud.Option) (alphaRow, error) {
	rows, err := core.GetAll(ctx, options...)
	if err != nil {
		return alphaRow{}, err
	}
	if len(rows) == 0 {
		return alphaRow{}, crud.ErrNotFound
	}
	return rows[0], nil
}

func (core *alphaCore) Save(ctx context.Context, model *alphaRow) (alphaRow, error) {
	tx, err := core.transaction(ctx)
	if err != nil {
		return alphaRow{}, err
	}
	core.mu.Lock()
	core.writeCalls++
	panicSave := core.panicSave
	core.mu.Unlock()
	row := *model
	if row.ID == 0 {
		row.ID = tx.nextID
		tx.nextID++
	}
	tx.rows[row.ID] = row
	if panicSave {
		panic("alpha save panic")
	}
	return row, nil
}

func (core *alphaCore) SaveOnly(ctx context.Context, model *alphaRow) error {
	_, err := core.Save(ctx, model)
	return err
}

func (core *alphaCore) SaveScoped(ctx context.Context, model *alphaRow, _ *crud.ScopedSave[alphaRow]) error {
	tx, err := core.transaction(ctx)
	if err != nil {
		return err
	}
	tx.rows[model.ID] = *model
	return nil
}

func (core *alphaCore) Update(ctx context.Context, id int64, dto any, _ ...crud.Option) (alphaRow, error) {
	tx, err := core.transaction(ctx)
	if err != nil {
		return alphaRow{}, err
	}
	row, ok := tx.rows[id]
	if !ok {
		return alphaRow{}, crud.ErrNotFound
	}
	patch, ok := dto.(alphaPatch)
	if !ok {
		return alphaRow{}, crud.ErrBadRequest
	}
	row.Name = patch.Name
	tx.rows[id] = row
	return row, nil
}

func (*alphaCore) UpdateAll(context.Context, any, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (core *alphaCore) Aggregate(ctx context.Context, _ ...crud.Option) ([]crud.AggregateRow, error) {
	rows, err := core.rows(ctx)
	if err != nil {
		return nil, err
	}
	return []crud.AggregateRow{{Value: map[string]any{"count": int64(len(rows))}}}, nil
}

func (*alphaCore) SaveAll(context.Context, []*alphaRow) error { return audit.ErrUnsupported }

func (core *alphaCore) Delete(ctx context.Context, ids ...int64) (int64, error) {
	return core.DeleteScoped(ctx, &crud.ScopedDelete[int64]{IDs: ids})
}

func (core *alphaCore) DeleteScoped(ctx context.Context, deletion *crud.ScopedDelete[int64]) (int64, error) {
	tx, err := core.transaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	for _, id := range deletion.IDs {
		if _, ok := tx.rows[id]; ok {
			delete(tx.rows, id)
			count++
		}
	}
	return count, nil
}

func (*alphaCore) DeleteAll(context.Context, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (core *alphaCore) Count(ctx context.Context, _ ...crud.Option) (int64, error) {
	rows, err := core.rows(ctx)
	return int64(len(rows)), err
}

func (core *alphaCore) Exists(ctx context.Context, _ ...crud.Option) (bool, error) {
	rows, err := core.rows(ctx)
	return len(rows) != 0, err
}

func (core *alphaCore) ExistsUnscoped(ctx context.Context, options ...crud.Option) (bool, error) {
	return core.Exists(ctx, options...)
}

func (core *alphaCore) Tx(ctx context.Context, fn func(context.Context) error) error {
	core.mu.Lock()
	unbound := core.unboundTx
	core.mu.Unlock()
	if unbound {
		return fn(ctx)
	}
	if _, bound, err := crud.SourceBoundExecutorFor(ctx, core.source); err != nil {
		return err
	} else if bound {
		return fn(ctx)
	}
	value, err := core.source.Begin(ctx)
	if err != nil {
		return err
	}
	tx := value.(*alphaTx)
	transactionContext := crud.BindExecutor(ctx, core.source, tx)
	if err := fn(transactionContext); err != nil {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (core *alphaCore) rows(ctx context.Context) (map[int64]alphaRow, error) {
	if executor, bound, err := crud.SourceBoundExecutorFor(ctx, core.source); err != nil {
		return nil, err
	} else if bound {
		tx, ok := executor.(*alphaTx)
		if !ok || tx.source != core.source || !tx.InTransaction() {
			return nil, errors.New("alpha core received a foreign transaction")
		}
		return cloneAlphaRows(tx.rows), nil
	}
	rows, _ := core.source.database.snapshot()
	return rows, nil
}

func (core *alphaCore) transaction(ctx context.Context) (*alphaTx, error) {
	executor, bound, err := crud.SourceBoundExecutorFor(ctx, core.source)
	if err != nil {
		return nil, err
	}
	tx, ok := executor.(*alphaTx)
	if !bound || !ok || tx.source != core.source || !tx.InTransaction() {
		return nil, errors.New("alpha mutation escaped its transaction")
	}
	return tx, nil
}

func (core *alphaCore) seed(rows ...alphaRow) {
	core.source.database.mu.Lock()
	defer core.source.database.mu.Unlock()
	for _, row := range rows {
		core.source.database.rows[row.ID] = row
		if row.ID >= core.source.database.nextID {
			core.source.database.nextID = row.ID + 1
		}
	}
}

func (core *alphaCore) committedRows() map[int64]alphaRow {
	rows, _ := core.source.database.snapshot()
	return rows
}

func (core *alphaCore) readCount() int {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.readCalls
}

func (core *alphaCore) writeCount() int {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.writeCalls
}

func (core *alphaCore) settlements() (int, int, int) {
	core.source.database.mu.Lock()
	defer core.source.database.mu.Unlock()
	return core.source.database.beginCalls, core.source.database.commits, core.source.database.rollbacks
}

func cloneAlphaRows(input map[int64]alphaRow) map[int64]alphaRow {
	result := make(map[int64]alphaRow, len(input))
	for id, row := range input {
		result[id] = row
	}
	return result
}

func (writer *alphaWriter) Capabilities() audit.Capabilities  { return writer.caps }
func (writer *alphaWriter) Limits() audit.Limits              { return writer.limits }
func (writer *alphaWriter) Backing() audit.Backing            { return writer.backing }
func (writer *alphaWriter) BackingID() audit.BackingID        { return writer.backingID }
func (writer *alphaWriter) LogID() audit.LogID                { return writer.logID }
func (writer *alphaWriter) Catalogs() audit.StoreCatalogState { return writer.state }
func (writer *alphaWriter) TransactionSource() any            { return writer.source }

func (writer *alphaWriter) BindTransaction(executor crud.Executor) (audit.Execution, error) {
	tx, ok := executor.(*alphaTx)
	if !ok || tx.source != writer.source || !tx.InTransaction() {
		return nil, audit.Failure(audit.Refused, errors.New("foreign alpha transaction"))
	}
	authority, err := audit.AuthorityFor(writer.source.database, tx)
	if err != nil {
		return nil, err
	}
	return &alphaExecution{writer: writer, tx: tx, authority: authority}, nil
}

func (*alphaWriter) LookupIdempotency(context.Context, audit.IdempotencyLookupRequest) (audit.IdempotencyLookupResult, error) {
	return audit.IdempotencyLookupResult{}, audit.Failure(audit.Refused, errors.New("lookup is outside the alpha fixture"))
}

func (*alphaWriter) Append(context.Context, audit.AppendRequest) (audit.AppendResult, error) {
	return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("standalone append is outside the alpha fixture"))
}

func (writer *alphaWriter) Lookup(ctx context.Context, request audit.LookupRequest) (audit.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.LookupResult{}, err
	}
	view := request.View()
	writer.mu.Lock()
	stored, ok := writer.stored[view.Revision]
	writer.mu.Unlock()
	if !ok {
		return audit.NewLookupResult(request, audit.LookupResultData{State: audit.AbsentNow, Visibility: audit.Committed})
	}
	return audit.NewLookupResult(request, audit.LookupResultData{State: audit.Found, Stored: stored, Visibility: audit.Committed})
}

func (writer *alphaWriter) fail(err error) {
	writer.mu.Lock()
	writer.failNext = err
	writer.mu.Unlock()
}

func (writer *alphaWriter) committedRevisions() []audit.RevisionWireView {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	result := make([]audit.RevisionWireView, len(writer.revisions))
	copy(result, writer.revisions)
	return result
}

func (execution *alphaExecution) Authority() audit.Authority { return execution.authority }

func (execution *alphaExecution) EntityHead(ctx context.Context, request audit.EntityHeadRequest) (audit.EntityHeadResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.EntityHeadResult{}, err
	}
	if !execution.tx.InTransaction() {
		return audit.EntityHeadResult{}, audit.Failure(audit.Refused, errors.New("settled alpha transaction"))
	}
	view := request.View()
	execution.writer.mu.Lock()
	head, exists := execution.writer.heads[view.Candidate]
	execution.writer.mu.Unlock()
	data := audit.EntityHeadResultData{State: audit.EntityGenesis, Chain: view.Candidate, Authority: execution.authority}
	if exists {
		data.State = audit.EntityExisting
		data.Previous = head.previous
		if head.terminal {
			data.State = audit.EntityTerminal
		}
	}
	return audit.NewEntityHeadResult(request, data)
}

func (execution *alphaExecution) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AppendResult{}, err
	}
	if !execution.tx.InTransaction() {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("settled alpha transaction"))
	}
	execution.writer.mu.Lock()
	if execution.writer.failNext != nil {
		cause := execution.writer.failNext
		execution.writer.failNext = nil
		execution.writer.mu.Unlock()
		return audit.AppendResult{}, audit.Failure(audit.NotWritten, cause)
	}
	execution.writer.sequence++
	sequence := execution.writer.sequence
	execution.writer.mu.Unlock()
	positionBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(positionBytes, sequence)
	position, err := audit.NewStorePosition(positionBytes)
	if err != nil {
		return audit.AppendResult{}, err
	}
	view := request.View()
	stored, err := audit.NewStoredHeader(audit.StoredHeaderData{
		Header: view.Revision.View().Header, Intent: view.Intent,
		RecordedAt: time.Unix(1_800_000_000+int64(sequence), 0).UTC(), Position: position,
	})
	if err != nil {
		return audit.AppendResult{}, err
	}
	result, err := audit.NewAppendResult(request, stored, audit.Inserted, execution.authority)
	if err != nil {
		return audit.AppendResult{}, err
	}
	execution.tx.mu.Lock()
	execution.tx.pending = append(execution.tx.pending, alphaPendingAppend{request: request, stored: stored})
	execution.tx.mu.Unlock()
	return result, nil
}
