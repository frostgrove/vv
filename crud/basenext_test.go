package crud_test

import (
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

var walkArticles = sqlrepo.Define[Article, int64, struct{}]("articles")

type auditing struct{ crud.Base[Article, int64] }

func auditingLayer() crud.Middleware[Article, int64] {
	return func(next crud.Core[Article, int64]) crud.Core[Article, int64] {
		return auditing{crud.Base[Article, int64]{Core: next}}
	}
}

type handRolled struct{ crud.Core[Article, int64] }

func handRolledLayer() crud.Middleware[Article, int64] {
	return func(next crud.Core[Article, int64]) crud.Core[Article, int64] {
		return handRolled{next}
	}
}

func TestADecoratorBuiltOnBaseIsStillWalkableToItsDatasource(t *testing.T) {
	rec := crudtest.Postgres()

	if source, ok := crud.SourceOf(walkArticles.Bind(rec).Unwrap()); !ok || source != crud.Source(rec) {
		t.Fatal("the repository does not answer with its own datasource, so nothing here proves anything about decorators")
	}

	repository := walkArticles.Bind(rec, auditingLayer(), auditingLayer())
	source, ok := crud.SourceOf(repository.Unwrap())
	if !ok {
		t.Fatal("a decorator built on crud.Base hid the datasource underneath it — a probe wired above it would refuse at start-up")
	}
	if source != crud.Source(rec) {
		t.Fatalf("the walk answered with %T, want the source the repository was bound to", source)
	}

	deaf := walkArticles.Bind(rec, handRolledLayer())
	if _, ok := crud.SourceOf(deaf.Unwrap()); ok {
		t.Fatal("a decorator that says nothing about what it wraps was walked through anyway — the walk is guessing, not following Next()")
	}

	mixed := walkArticles.Bind(rec, auditingLayer(), handRolledLayer())
	if _, ok := crud.SourceOf(mixed.Unwrap()); ok {
		t.Fatal("the walk reached past a deaf layer sitting under a Base one")
	}
}

type looping struct{ crud.Base[Article, int64] }

func (this *looping) Next() crud.Core[Article, int64] { return this }

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
