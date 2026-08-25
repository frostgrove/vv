//go:build integration

package integration

import (
	"time"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/sqlrepo"
)

// The blog schema exercises every relation kind at once: belongs_to, has_many,
// a two-hop path, and many_to_many through a join table.

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

// ArticleStats is the has_one end: the child holds the foreign key and there is
// at most one of it. The table deliberately carries no unique index on
// article_id — see TestAHasOneWithTwoMatchesPicksTheSameRowEveryTime, which is
// about what the library does when a schema does not keep that promise.
type ArticleStats struct {
	ID        int64 `db:"id,pk,auto"`
	ArticleID int64 `db:"article_id"`
	WordCount int   `db:"word_count"`
}

type Article struct {
	ID          int64               `db:"id,pk,auto"`
	AuthorID    int64               `db:"author_id"`
	Title       string              `db:"title"`
	Body        string              `db:"body"`
	Views       int                 `db:"views"`
	PublishedAt crud.Opt[time.Time] `db:"published_at"`
	CreatedAt   time.Time           `db:"created_at,generated"`

	Author   *Author       `rel:"belongs_to"`
	Stats    *ArticleStats `rel:"has_one"`
	Comments []Comment     `rel:"has_many"`
	Tags     []Tag         `rel:"many_to_many,join=article_tags"`
}

type ArticleUpdate struct {
	Title       *string
	Body        *string
	Views       *int
	PublishedAt crud.Opt[time.Time]
}

type CommentUpdate struct {
	Body     *string
	Approved *bool
}

var (
	Authors  = sqlrepo.Define[Author, int64, struct{}]("authors")
	Articles = sqlrepo.Define[Article, int64, ArticleUpdate]("articles")
	Comments = sqlrepo.Define[Comment, int64, CommentUpdate]("comments")
	Tags     = sqlrepo.Define[Tag, int64, struct{}]("tags")
	Stats    = sqlrepo.Define[ArticleStats, int64, struct{}]("article_stats")
)

const schemaBlogPostgres = `
DROP TABLE IF EXISTS article_stats;
DROP TABLE IF EXISTS article_tags;
DROP TABLE IF EXISTS comments;
DROP TABLE IF EXISTS articles;
DROP TABLE IF EXISTS authors;
DROP TABLE IF EXISTS tags;

CREATE TABLE authors (
	id   BIGSERIAL PRIMARY KEY,
	name VARCHAR(255) NOT NULL
);
CREATE TABLE tags (
	id   BIGSERIAL PRIMARY KEY,
	slug VARCHAR(64) NOT NULL
);
CREATE TABLE articles (
	id           BIGSERIAL PRIMARY KEY,
	author_id    BIGINT       NOT NULL,
	title        VARCHAR(255) NOT NULL,
	body         TEXT         NOT NULL DEFAULT '',
	views        INTEGER      NOT NULL DEFAULT 0,
	published_at TIMESTAMPTZ      NULL,
	created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE TABLE comments (
	id         BIGSERIAL PRIMARY KEY,
	article_id BIGINT  NOT NULL,
	author_id  BIGINT  NOT NULL,
	body       TEXT    NOT NULL,
	approved   BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE article_tags (
	article_id BIGINT NOT NULL,
	tag_id     BIGINT NOT NULL,
	PRIMARY KEY (article_id, tag_id)
);
CREATE TABLE article_stats (
	id         BIGSERIAL PRIMARY KEY,
	article_id BIGINT  NOT NULL,
	word_count INTEGER NOT NULL DEFAULT 0
);`

var schemaBlogMySQL = []string{
	`DROP TABLE IF EXISTS article_stats`,
	`DROP TABLE IF EXISTS article_tags`,
	`DROP TABLE IF EXISTS comments`,
	`DROP TABLE IF EXISTS articles`,
	`DROP TABLE IF EXISTS authors`,
	`DROP TABLE IF EXISTS tags`,
	`CREATE TABLE authors (
		id   BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
		name VARCHAR(255) NOT NULL
	) ENGINE=InnoDB`,
	`CREATE TABLE tags (
		id   BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
		slug VARCHAR(64) NOT NULL
	) ENGINE=InnoDB`,
	`CREATE TABLE articles (
		id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
		author_id    BIGINT       NOT NULL,
		title        VARCHAR(255) NOT NULL,
		body         TEXT         NOT NULL,
		views        INT          NOT NULL DEFAULT 0,
		published_at DATETIME(6)      NULL,
		created_at   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
	) ENGINE=InnoDB`,
	`CREATE TABLE comments (
		id         BIGINT  NOT NULL AUTO_INCREMENT PRIMARY KEY,
		article_id BIGINT  NOT NULL,
		author_id  BIGINT  NOT NULL,
		body       TEXT    NOT NULL,
		approved   BOOLEAN NOT NULL DEFAULT FALSE
	) ENGINE=InnoDB`,
	`CREATE TABLE article_tags (
		article_id BIGINT NOT NULL,
		tag_id     BIGINT NOT NULL,
		PRIMARY KEY (article_id, tag_id)
	) ENGINE=InnoDB`,
	`CREATE TABLE article_stats (
		id         BIGINT  NOT NULL AUTO_INCREMENT PRIMARY KEY,
		article_id BIGINT  NOT NULL,
		word_count INT     NOT NULL DEFAULT 0
	) ENGINE=InnoDB`,
}
