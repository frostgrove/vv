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

type Article struct {
	ID          int64               `db:"id,pk,auto"`
	AuthorID    int64               `db:"author_id,immutable"`
	Title       string              `db:"title"`
	Body        string              `db:"body"`
	Views       int                 `db:"views"`
	PublishedAt crud.Opt[time.Time] `db:"published_at"`
	CreatedAt   time.Time           `db:"created_at,generated"`
}

type ArticleUpdate struct {
	Title       *string             `json:"title,omitempty"`
	Body        *string             `json:"body,omitempty"`
	PublishedAt crud.Opt[time.Time] `json:"publishedAt,omitzero"`
}

var Articles = sqlrepo.Define[Article, int64, ArticleUpdate]("articles",
	sqlrepo.DefaultLimit(20),
	sqlrepo.MaxLimit(100),
	sqlrepo.DefaultSort(crud.Desc("CreatedAt")),
)

type articleAttrs struct {
	ID          specs.Ord[Article, int64]
	AuthorID    specs.Ord[Article, int64]
	Title       specs.Str[Article]
	Body        specs.Str[Article]
	Views       specs.Ord[Article, int]
	PublishedAt specs.Cmp[Article, time.Time]
}

var Article_ = specs.Metamodel[Article, articleAttrs]()

func Published() specs.Specification[Article] {
	return Article_.PublishedAt.NotNull()
}

func Popular(min int) specs.Specification[Article] {
	return specs.Of[Article](func(root specs.Root[Article], cb specs.Builder) crud.Predicate {
		return cb.GreaterThanOrEqualTo(root.Get("Views"), min)
	})
}

func Trending() specs.Specification[Article] {
	return specs.Where(Published()).And(Popular(1000))
}

type authorKey struct{}

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

var OwnPolicy = security.ScopeField[Article, int64]("AuthorID", authorOf)

type Repo struct {
	*specs.Repo[Article, int64, ArticleUpdate]
}

func Open(source crud.Source) Repo {
	return Repo{specs.Executor(Articles.Bind(source, security.Gate(OwnPolicy)))}
}

func (this Repo) Publish(ctx context.Context, id int64, at time.Time) (Article, error) {
	a, err := this.Update(ctx, id, ArticleUpdate{PublishedAt: crud.Set(at)})
	if errors.Is(err, crud.ErrNotFound) {
		return Article{}, err
	}
	return a, err
}

func (this Repo) Unpublish(ctx context.Context, id int64) (Article, error) {
	return this.Update(ctx, id, ArticleUpdate{PublishedAt: crud.Null[time.Time]()})
}

func (this Repo) Feed(ctx context.Context, page int) (crud.PaginatedResponse[Article], error) {
	return this.FindPage(ctx, Trending(), crud.Page(page), crud.Limit(20),
		crud.OrderBy(Article_.Views.Desc()))
}
