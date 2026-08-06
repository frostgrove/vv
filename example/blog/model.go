// Package blog is a worked example of the generator: model.go is what you
// write, rxcrud_gen.go is what rxcrud produces from it. The test next door
// regenerates and diffs, so the two can never drift.
package blog

//go:generate go run rx-crud/cmd/rxcrud

import (
	"time"

	"rx-crud/crud"
)

type Author struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`

	Articles []Article `rel:"has_many"`
}

type Tag struct {
	ID   int64  `db:"id,pk,auto"`
	Slug string `db:"slug"`
}

type Comment struct {
	ID        int64  `db:"id,pk,auto"`
	ArticleID int64  `db:"article_id"`
	AuthorID  int64  `db:"author_id"`
	Body      string `db:"body"`
	Approved  bool   `db:"approved"`

	Author *Author `rel:"belongs_to"`
}

type Article struct {
	ID          int64               `db:"id,pk,auto"`
	AuthorID    int64               `db:"author_id"`
	Title       string              `db:"title"`
	Body        string              `db:"body"`
	Views       int                 `db:"views"`
	Rating      *float64            `db:"rating"`
	PublishedAt crud.Opt[time.Time] `db:"published_at"`
	TenantID    int64               `db:"tenant_id,immutable"`
	CreatedAt   time.Time           `db:"created_at,generated"`
	Secret      string              `db:"-"`

	Author   *Author   `rel:"belongs_to"`
	Comments []Comment `rel:"has_many"`
	Tags     []Tag     `rel:"many_to_many,join=article_tags"`
}
