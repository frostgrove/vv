package crud_test

import (
	"testing"
	"time"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/crud/sqlrepo"
)

// crud.Base is what docs/modules/en/crud.md tells a consumer to embed, and the
// reason it gives is this one: "it supplies the Next() that crud.SourceOf
// walks". The decorators inside this library each write their own Next() by
// hand, so nothing else in the tree exercises Base's — the advice a stranger
// follows is the part that had no test.
//
// What the advice buys is not visible from the decorator: a probe wired above
// it looks for the datasource by walking down, and a layer that cannot say what
// it wraps ends that walk. An interface embedded in a struct promotes only its
// own method set, so the erasure is silent and compiles.

var walkArticles = sqlrepo.Define[Article, int64, struct{}]("walk_articles")

// auditing is the shape the documentation describes: embed Base, override
// nothing or one method, forward the rest.
type auditing struct{ crud.Base[Article, int64] }

func auditingLayer() crud.Middleware[Article, int64] {
	return func(next crud.Core[Article, int64]) crud.Core[Article, int64] {
		return auditing{crud.Base[Article, int64]{Core: next}}
	}
}

// handRolled is the same decorator written without Base — it holds the Core
// interface directly. It forwards all eleven methods, compiles, and passes
// every functional test a consumer would write for it.
type handRolled struct{ crud.Core[Article, int64] }

func handRolledLayer() crud.Middleware[Article, int64] {
	return func(next crud.Core[Article, int64]) crud.Core[Article, int64] {
		return handRolled{next}
	}
}

// A decorator built on Base keeps the chain walkable: the datasource is found
// through it, from any depth.
func TestADecoratorBuiltOnBaseIsStillWalkableToItsDatasource(t *testing.T) {
	rec := crudtest.Postgres()

	// First that there is something to find at all, or neither half below says
	// anything: the repository itself answers with the source it was bound to.
	if src, ok := crud.SourceOf(walkArticles.Bind(rec).Unwrap()); !ok || src != crud.Source(rec) {
		t.Fatal("the repository does not answer with its own datasource, so nothing here proves anything about decorators")
	}

	// Two layers, so this is a walk and not a single hop past the outermost.
	repo := walkArticles.Bind(rec, auditingLayer(), auditingLayer())
	src, ok := crud.SourceOf(repo.Unwrap())
	if !ok {
		t.Fatal("a decorator built on crud.Base hid the datasource underneath it — a probe wired above it would refuse at start-up")
	}
	if src != crud.Source(rec) {
		t.Fatalf("the walk answered with %T, want the source the repository was bound to", src)
	}

	// The control. Every assertion above would hold for a walk that reached
	// past any decorator whatsoever, and then Base would be advice with nothing
	// behind it. The decorator that does not say what it wraps has to end the
	// walk — that loss is what makes embedding Base worth recommending.
	deaf := walkArticles.Bind(rec, handRolledLayer())
	if _, ok := crud.SourceOf(deaf.Unwrap()); ok {
		t.Fatal("a decorator that says nothing about what it wraps was walked through anyway — the walk is guessing, not following Next()")
	}

	// And Base still ends the walk honestly when what it wraps is deaf: the
	// answer is "I cannot say", not the wrong source.
	mixed := walkArticles.Bind(rec, auditingLayer(), handRolledLayer())
	if _, ok := crud.SourceOf(mixed.Unwrap()); ok {
		t.Fatal("the walk reached past a deaf layer sitting under a Base one")
	}
}

// looping is a chain somebody built by accident: its Next returns itself. The
// walk has to end rather than run forever, because a decorator chain is
// assembled at start-up and a hang there is a process that never serves.
type looping struct{ crud.Base[Article, int64] }

func (l *looping) Next() crud.Core[Article, int64] { return l }

func TestAChainThatWalksBackIntoItselfEndsRatherThanHangs(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		_, ok := crud.SourceOf[Article, int64](&looping{})
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("a chain that never reaches a repository claimed to have found a datasource")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the walk is still following a cycle it will never leave — the depth bound is gone")
	}
}
