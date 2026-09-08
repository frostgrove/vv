package tenancyjobs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	"github.com/frostgrove/vv/tenancy"
)

type recordingSender struct {
	inner  jobs.Sender
	placed []jobs.Placement
}

func (this *recordingSender) Description() jobs.BackendDescription { return this.inner.Description() }

func (this *recordingSender) Place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	this.placed = append(this.placed, placement)
	return this.inner.Place(ctx, placement)
}

type tenantQueue struct {
	definition jobs.Name
	namespace  jobs.Namespace
	policy     jobs.TracePolicy
	automatic  *jobs.Automatic[string]
	sender     *recordingSender
}

func tenantWork(t *testing.T, authority *tenancy.Authority, definition string) tenantQueue {
	t.Helper()
	name, err := jobs.ParseName(definition)
	if err != nil {
		t.Fatal(err)
	}
	automatic := jobs.MustWire(jobs.Declare[string](), jobs.WireSpec[string]{
		Name:      name,
		Codec:     jobs.String(jobs.SchemaVersion(1)),
		Partition: jobs.PartitionTenantRequired,
	})
	catalog, err := jobs.NewCatalog(automatic)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := jobs.NamespaceOf("billing", "test")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := jobs.ParseIdentityProvenance("tenancy.test")
	if err != nil {
		t.Fatal(err)
	}
	trustEpoch, err := jobs.NewIdentityEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := ContextProvider(authority, provenance, trustEpoch)
	if err != nil {
		t.Fatalf("job context: %v", err)
	}
	backend, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{inner: backend}
	queue, err := jobs.NewQueue(jobs.QueueSpec{
		Namespace: namespace, Catalog: catalog, Sender: sender,
		Context: provider, Entropy: bytes.NewReader(make([]byte, 1024)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Activate(); err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.NewTracePolicy()
	if err != nil {
		t.Fatal(err)
	}
	return tenantQueue{definition: name, namespace: namespace, policy: policy, automatic: automatic, sender: sender}
}

func (this tenantQueue) restoreRequest(t *testing.T) jobs.IdentityRestoreRequest {
	t.Helper()
	if len(this.sender.placed) != 1 {
		t.Fatalf("the queue placed %d jobs", len(this.sender.placed))
	}
	placement := this.sender.placed[0]
	request, err := placement.Context().IdentityRestoreRequest(
		this.namespace, placement.Partition(), this.definition,
		placement.Candidate(), placement.WireDigest(), this.policy)
	if err != nil {
		t.Fatalf("the record the producer wrote cannot be read back: %v", err)
	}
	return request
}

// Both constructors exist to turn a wiring mistake into a boot failure. A
// deployment that reaches production without a durable key has a queue table
// whose tenant column anything with write access can choose.
func TestASeamWithNothingToAuthenticateWithRefusesToExist(t *testing.T) {
	reference, err := tenancy.ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	keyless, err := tenancy.New(tenancy.Spec{
		Resolver: tenancy.Fixed{Reference: reference, Lifecycle: tenancy.Active, Epoch: epoch},
	})
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := jobs.ParseIdentityProvenance("tenancy.test")
	if err != nil {
		t.Fatal(err)
	}
	trustEpoch, err := jobs.NewIdentityEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	keyed := authorityOnly(t, "acme", tenancy.Active, 1)

	for name, built := range map[string]func() (any, error){
		"a producer with no durable key": func() (any, error) {
			return ContextProvider(keyless, provenance, trustEpoch)
		},
		"a worker with no durable key": func() (any, error) {
			return IdentityRestorer(keyless)
		},
		"a producer with no authority at all": func() (any, error) {
			return ContextProvider(nil, provenance, trustEpoch)
		},
		"a producer that names no provenance": func() (any, error) {
			return ContextProvider(keyed, jobs.IdentityProvenance{}, trustEpoch)
		},
		"a producer that names no trust epoch": func() (any, error) {
			return ContextProvider(keyed, provenance, jobs.IdentityEpoch(0))
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := built(); err == nil {
				t.Fatal("the seam was constructed, so a wiring mistake becomes a runtime one")
			}
		})
	}

	if _, err := ContextProvider(keyed, provenance, trustEpoch); err != nil {
		t.Fatalf("the control refuses too, so the assertions above prove nothing: %v", err)
	}
	if _, err := IdentityRestorer(keyed); err != nil {
		t.Fatalf("the control refuses too, so the assertions above prove nothing: %v", err)
	}
}

// Work that needs no tenant still runs. The restorer answers the context it was
// handed, unbound, rather than refusing a record that carries no partition —
// a deployment mixing tenanted and untenanted jobs has one restorer.
func TestWorkThatNeedsNoTenantRestoresWithNoneBound(t *testing.T) {
	authority := authorityOnly(t, "acme", tenancy.Active, 1)
	namespace, err := jobs.NamespaceOf("billing", "test")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := jobs.ParseName("nightly-report")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.NewTracePolicy()
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := jobs.ParseIdentityProvenance("tenancy.test")
	if err != nil {
		t.Fatal(err)
	}
	trustEpoch, err := jobs.NewIdentityEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := jobs.NewContextCapture(jobs.ContextCaptureSpec{Provenance: provenance, Epoch: trustEpoch})
	if err != nil {
		t.Fatal(err)
	}
	key, durable, err := jobs.BuildDurableContext(namespace, definition, jobs.PartitionGlobal, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	request, err := durable.IdentityRestoreRequest(namespace, key, definition, someInvocation(t), someWire(t, 1), policy)
	if err != nil {
		t.Fatal(err)
	}
	if request.Scope() == jobs.ContextTenant {
		t.Fatal("a global job wrote a tenant record, so this proves nothing about the untenanted path")
	}

	// The worker's own context is bound, which is the case that matters and the
	// only one that can fail: restoring from a background context proves the
	// branch returns a context, not that it strips the one it was handed. An
	// in-process worker started under a bound request, or a harness that bound
	// once at start-up, arrives here exactly like this.
	worker, err := authority.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, bound := tenancy.From(worker); !bound {
		t.Fatal("the worker context is not bound, so this test cannot see the inheritance it exists for")
	}

	restored, err := jobs.RestoreTrustedIdentity(worker, restorer(t, authority), request)
	if err != nil {
		t.Fatalf("work that needs no tenant was refused: %v", err)
	}
	if _, bound := tenancy.From(restored); bound {
		t.Fatal("an untenanted job inherited the tenant the worker context happened to carry, " +
			"and every tenancy seam inside its handler now answers for that tenant")
	}
}

func TestAProducerWithNoTenantEnqueuesNothing(t *testing.T) {
	authority, _ := fixedAuthority(t, "acme", tenancy.Active, 1)
	work := tenantWork(t, authority, "unscoped-producer")

	err := jobs.Go(context.Background(), work.automatic, "invoice-1")
	if err == nil {
		t.Fatal("work requiring a tenant partition was enqueued with no tenant in the context")
	}
	if len(work.sender.placed) != 0 {
		t.Fatalf("%d jobs reached the backend", len(work.sender.placed))
	}
}

func TestWorkEnqueuedByATenantIsExecutedAsThatTenant(t *testing.T) {
	authority, scope := fixedAuthority(t, "acme", tenancy.Active, 1)
	work := tenantWork(t, authority, "captured-producer")

	ctx, err := authority.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Go(ctx, work.automatic, "invoice-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	request := work.restoreRequest(t)
	if request.Scope() != jobs.ContextTenant {
		t.Fatalf("the producer wrote a %v record for work that requires a tenant", request.Scope())
	}

	restored, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, authority), request)
	if err != nil {
		t.Fatalf("the worker refused work its own tenant enqueued: %v", err)
	}
	carried, ok := tenancy.From(restored)
	if !ok || carried.Reference().Value() != "acme" {
		t.Fatalf("the handler's context carries %v", carried)
	}
}

func TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt(t *testing.T) {
	producer, scope := fixedAuthority(t, "acme", tenancy.Active, 1)
	work := tenantWork(t, producer, "suspended-producer")

	ctx, err := producer.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Go(ctx, work.automatic, "invoice-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	request := work.restoreRequest(t)

	if _, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, producer), request); err != nil {
		t.Fatalf("the control refuses too, so the assertions below prove nothing: %v", err)
	}

	for name, worker := range map[string]*tenancy.Authority{
		"suspended since enqueue":      authorityOnly(t, "acme", tenancy.Suspended, 1),
		"deleted since enqueue":        authorityOnly(t, "acme", tenancy.Deleted, 1),
		"restored to a new generation": authorityOnly(t, "acme", tenancy.Active, 2),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, worker), request); err == nil {
				t.Fatal("the backlog outlived the control-plane decision and the handler ran")
			}
		})
	}
}

// The producer asked whether this tenant may enqueue; the worker asks again,
// because the answer is a control-plane state that moves while the record sits in
// the queue. A deployment that keeps a suspended tenant readable and admits it no
// durable work is what tells the two questions apart: a restorer that asked for
// reads instead would run the backlog of a suspended tenant, which is the whole
// reason the class is re-asked here.
func TestTheDurableClassIsAskedAgainWhenTheRecordIsRestored(t *testing.T) {
	producer, scope := fixedAuthority(t, "acme", tenancy.Active, 1)
	work := tenantWork(t, producer, "readable-but-not-durable")
	ctx, err := producer.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Go(ctx, work.automatic, "invoice-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	request := work.restoreRequest(t)

	readable := tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).
		Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)).
		Merge(tenancy.Admit(tenancy.ClassDurable, tenancy.Active))

	for state, runs := range map[tenancy.Lifecycle]bool{tenancy.Suspended: false, tenancy.Active: true} {
		t.Run(state.String(), func(t *testing.T) {
			_, err := jobs.RestoreTrustedIdentity(context.Background(),
				restorer(t, admittedAuthority(t, "acme", state, 1, readable)), request)
			if runs != (err == nil) {
				t.Fatalf("err = %v — a tenant this deployment keeps readable and admits no durable work ran its backlog", err)
			}
		})
	}
}

func TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit(t *testing.T) {
	reference, err := tenancy.ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := tenancy.New(tenancy.Spec{
		Resolver:   tenancy.Fixed{Reference: reference, Lifecycle: tenancy.Active, Epoch: epoch},
		Admission:  tenancy.Admit(tenancy.ClassRead, tenancy.Active).Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)),
		DurableKey: durableTestKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	work := tenantWork(t, readOnly, "durable-refused-producer")

	scope, err := readOnly.Verify(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := readOnly.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	// The refusal reaches the caller as the queue's own driver error: `jobs`
	// collapses whatever a context provider returns, deliberately, so a provider
	// cannot choose the producer's error identity. What matters here is that the
	// work does not exist.
	if err := jobs.Go(ctx, work.automatic, "invoice-1"); err == nil {
		t.Fatal("a deployment that admits no durable work enqueued some")
	}
	if len(work.sender.placed) != 0 {
		t.Fatalf("%d jobs reached the backend", len(work.sender.placed))
	}

	if _, err := readOnly.Verify(context.Background(), tenancy.ClassDurable); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("the class the producer asks for answers %v, want tenancy.ErrInactive", err)
	}
}

func restorer(t *testing.T, authority *tenancy.Authority) jobs.TrustedIdentityRestorer {
	t.Helper()
	restore, err := IdentityRestorer(authority)
	if err != nil {
		t.Fatalf("job identity: %v", err)
	}
	return restore
}

func TestAForgedDurableRecordEntersNoHandler(t *testing.T) {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	acme, globex := mustReference(t, "acme"), mustReference(t, "globex")
	deployment, err := tenancy.New(tenancy.Spec{
		DurableKey: durableTestKey,
		Resolver: knownTenants{byReference: map[tenancy.Reference]tenancy.Resolution{
			acme:   {Reference: acme, Lifecycle: tenancy.Active, Epoch: epoch},
			globex: {Reference: globex, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	work := tenantWork(t, deployment, "forged-record")

	scope, err := deployment.Lookup(context.Background(), acme, tenancy.ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := deployment.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Go(ctx, work.automatic, "invoice-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, deployment), work.restoreRequest(t)); err != nil {
		t.Fatalf("the control refuses too, so the assertions below prove nothing: %v", err)
	}

	theirs, err := deployment.Lookup(context.Background(), globex, tenancy.ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	invocation, wire := someInvocation(t), someWire(t, 7)
	honest := sealed(t, deployment, work.namespace, work.definition, invocation, wire, theirs)
	if len(honest) != generationBytes+macBytes+len(globex.Value()) {
		t.Fatalf("a sealed record is %d bytes, so the forgeries below are shaped for a layout that no longer exists", len(honest))
	}
	if err := unsealed(t, deployment, work.namespace, work.definition, invocation, wire, honest); err != nil {
		t.Fatalf("the honest record these forgeries are built from does not itself verify: %v", err)
	}

	// Whoever can write the queue table can write any bytes into it. The records
	// below are exactly what they would write: the same claim, naming a tenant
	// that really exists and really is active, without the MAC that says this
	// deployment made it.
	for name, token := range map[string][]byte{
		"a hand-built plaintext claim":        handBuilt(globex, epoch),
		"an honest record with its MAC wiped": stripped(honest),
		"a claim carrying a random MAC":       scrambled(honest),
		"another deployment's key":            foreignRecord(t, work, globex, epoch, invocation, wire),
	} {
		t.Run(name, func(t *testing.T) {
			request := hostileRequest(t, work, globex, token, invocation, wire)
			if _, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, deployment), request); err == nil {
				t.Fatal("a record nobody with this deployment's key wrote was executed as the tenant it named")
			}
		})
	}
}

func someInvocation(t *testing.T) jobs.InvocationID {
	t.Helper()
	id, err := jobs.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func someWire(t *testing.T, seed byte) jobs.WireDigest {
	t.Helper()
	var value [32]byte
	for index := range value {
		value[index] = seed
	}
	digest, err := jobs.WireDigestFromBytes(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustReference(t *testing.T, raw string) tenancy.Reference {
	t.Helper()
	reference, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

const (
	generationBytes = 8
	macBytes        = 32
)

func sealed(t *testing.T, authority *tenancy.Authority, namespace jobs.Namespace, definition jobs.Name, invocation jobs.InvocationID, wire jobs.WireDigest, scope tenancy.Scope) []byte {
	t.Helper()
	sealer, err := authority.Sealer()
	if err != nil {
		t.Fatalf("the deployment cannot seal a record at all: %v", err)
	}
	token, err := sealer.Seal(context.Background(), scope, record(namespace, definition, invocation, wire)...)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func unsealed(t *testing.T, authority *tenancy.Authority, namespace jobs.Namespace, definition jobs.Name, invocation jobs.InvocationID, wire jobs.WireDigest, token []byte) error {
	t.Helper()
	sealer, err := authority.Sealer()
	if err != nil {
		t.Fatalf("the deployment cannot read a record at all: %v", err)
	}
	_, _, err = sealer.Unseal(token, record(namespace, definition, invocation, wire)...)
	return err
}

// What somebody with write access to the queue table writes when they do not know
// there is a MAC at all: the claim, and nothing else. It is shorter than a record,
// which is a different refusal from a wrong MAC and belongs to this seam because
// the queue accepts the bytes either way.
func handBuilt(reference tenancy.Reference, epoch tenancy.Epoch) []byte {
	claim := make([]byte, generationBytes)
	binary.BigEndian.PutUint64(claim, epoch.Value())
	return append(claim, reference.Value()...)
}

func stripped(token []byte) []byte {
	forged := append([]byte(nil), token...)
	clear(forged[generationBytes : generationBytes+macBytes])
	return forged
}

func scrambled(token []byte) []byte {
	forged := append([]byte(nil), token...)
	for i := generationBytes; i < generationBytes+macBytes; i++ {
		forged[i] = byte(i)
	}
	return forged
}

func otherDeployment(t *testing.T, reference tenancy.Reference, epoch tenancy.Epoch) *tenancy.Authority {
	t.Helper()
	other, err := tenancy.New(tenancy.Spec{
		DurableKey: []byte("a-different-durable-key-of-32-bytes!"),
		Resolver: knownTenants{byReference: map[tenancy.Reference]tenancy.Resolution{
			reference: {Reference: reference, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return other
}

// A second deployment, with its own durable key, sealing an honest record for a
// tenant it really knows — a record that travelled between environments rather
// than one somebody typed.
func foreignRecord(t *testing.T, work tenantQueue, reference tenancy.Reference, epoch tenancy.Epoch, invocation jobs.InvocationID, wire jobs.WireDigest) []byte {
	t.Helper()
	other := otherDeployment(t, reference, epoch)
	scope, err := other.Lookup(context.Background(), reference, tenancy.ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	return sealed(t, other, work.namespace, work.definition, invocation, wire, scope)
}

func hostileRequest(t *testing.T, work tenantQueue, reference tenancy.Reference, token []byte, invocation jobs.InvocationID, wire jobs.WireDigest) jobs.IdentityRestoreRequest {
	t.Helper()
	protected, err := jobs.NewProtectedIdentityToken(token)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := jobs.ParseIdentityProvenance("tenancy.test")
	if err != nil {
		t.Fatal(err)
	}
	trustEpoch, err := jobs.NewIdentityEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := jobs.NewContextCapture(jobs.ContextCaptureSpec{
		Tenant:     jobs.Partition(reference.Value()),
		Token:      protected,
		Provenance: provenance,
		Epoch:      trustEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, durable, err := jobs.BuildDurableContext(work.namespace, work.definition, jobs.PartitionTenantRequired, work.policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	request, err := durable.IdentityRestoreRequest(work.namespace, key, work.definition, invocation, wire, work.policy)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue(t *testing.T) {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	acme := mustReference(t, "acme")
	deployment, err := tenancy.New(tenancy.Spec{
		DurableKey: durableTestKey,
		Resolver: knownTenants{byReference: map[tenancy.Reference]tenancy.Resolution{
			acme: {Reference: acme, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	honest, err := jobs.NamespaceOf("billing", "test")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := jobs.NamespaceOf("billing", "staging")
	if err != nil {
		t.Fatal(err)
	}
	sending, err := jobs.ParseName("send-invoice")
	if err != nil {
		t.Fatal(err)
	}
	refunding, err := jobs.ParseName("issue-refund")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := deployment.Lookup(context.Background(), acme, tenancy.ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	invocation, wire := someInvocation(t), someWire(t, 3)
	token := sealed(t, deployment, honest, sending, invocation, wire, scope)

	if err := unsealed(t, deployment, honest, sending, invocation, wire, token); err != nil {
		t.Fatalf("the control refuses too, so the assertions below prove nothing: %v", err)
	}
	for name, replay := range map[string]struct {
		namespace  jobs.Namespace
		definition jobs.Name
		invocation jobs.InvocationID
		wire       jobs.WireDigest
	}{
		"into another queue":         {namespace: elsewhere, definition: sending, invocation: invocation, wire: wire},
		"into another job":           {namespace: honest, definition: refunding, invocation: invocation, wire: wire},
		"into another queue and job": {namespace: elsewhere, definition: refunding, invocation: invocation, wire: wire},
		// The two that matter most, because the queue and the job are the ones
		// the record was written for: a second row of the same job, and the same
		// row with the payload swapped underneath it.
		"onto another record of the same job":       {namespace: honest, definition: sending, invocation: someInvocation(t), wire: wire},
		"onto the same record with another payload": {namespace: honest, definition: sending, invocation: invocation, wire: someWire(t, 4)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := unsealed(t, deployment, replay.namespace, replay.definition, replay.invocation, replay.wire, token); !errors.Is(err, tenancy.ErrUntrusted) {
				t.Fatalf("err = %v, want tenancy.ErrUntrusted — a record was moved to somewhere it was never written for", err)
			}
		})
	}
}

// The replay this seam exists to stop, assembled the way the adversary the
// durable key names would assemble it: not a forged MAC, but an honest token
// lifted off a row the tenant really enqueued and reattached to a row the
// attacker wrote — same deployment, same queue, same job, same live tenant, a
// payload of their choosing. Nothing above covers it: every case in
// TestAForgedDurableRecordEntersNoHandler feeds a token nobody with the key
// wrote, and the replay table checks the queue and the job rather than the
// record. The control is the first assertion — the honest record restores — so
// a refusal that stopped happening for some unrelated reason fails here loudly
// rather than passing this test for free.
func TestAnHonestTokenReattachedToAnotherRecordEntersNoHandler(t *testing.T) {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	acme := mustReference(t, "acme")
	deployment, err := tenancy.New(tenancy.Spec{
		DurableKey: durableTestKey,
		Resolver: knownTenants{byReference: map[tenancy.Reference]tenancy.Resolution{
			acme: {Reference: acme, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	work := tenantWork(t, deployment, "send-invoice")
	scope, err := deployment.Lookup(context.Background(), acme, tenancy.ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := deployment.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Go(ctx, work.automatic, "invoice-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, deployment), work.restoreRequest(t)); err != nil {
		t.Fatalf("the tenant's own record does not restore, so the refusals below prove nothing: %v", err)
	}

	placement := work.sender.placed[0]
	stolen, ok := placement.Context().Token()
	if !ok {
		t.Fatal("the record the producer wrote carries no token, so there is nothing to replay")
	}
	for name, forged := range map[string]struct {
		invocation jobs.InvocationID
		wire       jobs.WireDigest
	}{
		"a new row carrying the tenant's token":        {invocation: someInvocation(t), wire: placement.WireDigest()},
		"the tenant's row with the payload swapped":    {invocation: placement.Candidate(), wire: someWire(t, 9)},
		"a new row and an attacker-chosen payload too": {invocation: someInvocation(t), wire: someWire(t, 11)},
	} {
		t.Run(name, func(t *testing.T) {
			request := hostileRequest(t, work, acme, stolen.Bytes(), forged.invocation, forged.wire)
			if _, err := jobs.RestoreTrustedIdentity(context.Background(), restorer(t, deployment), request); err == nil {
				t.Fatal("an honest token reattached to another record restored the tenant it was minted for")
			}
		})
	}
}
