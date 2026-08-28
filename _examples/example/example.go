// Package example is the whole of vv's user-facing surface in one file:
// a model, an update DTO, a repository declaration, a metamodel and a security
// policy. The tests next door run it against crud/crudtest, so what you read
// here is what the library actually produces.
package example

import (
	"context"
	"errors"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// Article is a typed view of the articles table.
type Article struct {
	ID          int64               `db:"id,pk,auto"`
	AuthorID    int64               `db:"author_id,immutable"`
	Title       string              `db:"title"`
	Body        string              `db:"body"`
	Views       int                 `db:"views"`
	PublishedAt crud.Opt[time.Time] `db:"published_at"` // nullable
	CreatedAt   time.Time           `db:"created_at,generated"`
}

// ArticleUpdate is the PATCH shape: field names match Article, pointers are
// optional, Opt is nullable-and-optional.
type ArticleUpdate struct {
	Title       *string             `json:"title,omitempty"`
	Body        *string             `json:"body,omitempty"`
	PublishedAt crud.Opt[time.Time] `json:"publishedAt,omitzero"`
}

// Articles is validated at package initialisation: tags, ID type and DTO.
var Articles = sqlrepo.Define[Article, int64, ArticleUpdate]("articles",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

// Article_ is the metamodel, JPA style. A renamed field breaks the build here
// rather than at query time.
type articleAttrs struct {
	ID          specs.Ord[Article, int64]
	AuthorID    specs.Ord[Article, int64]
	Title       specs.Str[Article]
	Body        specs.Str[Article]
	Views       specs.Ord[Article, int]
	PublishedAt specs.Cmp[Article, time.Time]
}

var Article_ = specs.Metamodel[Article, articleAttrs]()

// Published is a reusable specification.
func Published() specs.Specification[Article] {
	return Article_.PublishedAt.NotNull()
}

// Popular is the same thing written in the literal criteria-builder style.
func Popular(min int) specs.Specification[Article] {
	return specs.Of[Article](func(root specs.Root[Article], cb specs.Builder) crud.Predicate {
		return cb.GreaterThanOrEqualTo(root.Get("Views"), min)
	})
}

// Trending composes them.
func Trending() specs.Specification[Article] {
	return specs.Where(Published()).And(Popular(1000))
}

type authorKey struct{}

// WithAuthor puts the current principal into the context.
func WithAuthor(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, authorKey{}, id)
}

func authorOf(ctx context.Context) (any, error) {
	id, ok := ctx.Value(authorKey{}).(int64)
	if !ok {
		return nil, security.Denied(security.Read, "not signed in")
	}
	return id, nil
}

// OwnPolicy restricts everything to the signed-in author's own articles and
// freezes author_id.
var OwnPolicy = security.ScopeField[Article, int64]("AuthorID", authorOf)

// Repo is the fully decorated repository a handler would hold: scoped by
// author, with the specification surface on top.
type Repo struct {
	*specs.Repo[Article, int64, ArticleUpdate]
}

// Open wires everything together. This is the whole set-up cost.
func Open(src crud.Source) Repo {
	return Repo{specs.Executor(Articles.Bind(src, security.Gate(OwnPolicy)))}
}

// Publish is what a handler looks like: no SQL, no mapping, no manual diffing.
func (r Repo) Publish(ctx context.Context, id int64, at time.Time) (Article, error) {
	a, err := r.Update(ctx, id, ArticleUpdate{PublishedAt: crud.Set(at)})
	if errors.Is(err, crud.ErrNotFound) {
		return Article{}, err // out of scope articles look missing, by design
	}
	return a, err
}

// Unpublish clears the column instead of leaving it alone — the distinction Opt
// exists for.
func (r Repo) Unpublish(ctx context.Context, id int64) (Article, error) {
	return r.Update(ctx, id, ArticleUpdate{PublishedAt: crud.Null[time.Time]()})
}

// Feed is a paginated, specification-filtered read.
func (r Repo) Feed(ctx context.Context, page int) (crud.PaginatedResponse[Article], error) {
	return r.FindPage(ctx, Trending(), crud.Page(page), crud.Limit(20),
		crud.OrderBy(Article_.Views.Desc()))
}
