//go:build integration

package integration

import (
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/repo/basic"
)

// The fixture the probe tests read.
//
// It is its own schema for the reason catSchema is: egSchema's `users` has no
// unique key beyond its primary one, no foreign key and no child table, which is
// three of the four things a probe is for. And the four engines disagree about
// which unique key they cannot replay from a value, so the twin has to be
// declared per engine under one shared predicate.
//
// What each column is for, and every one of them carries its own opposite:
//
//   - email — a plain unique key. The one real violation the negative twin
//     leaves in its payload.
//   - code — a unique key another table's foreign key points at, so an update
//     that changes it is refused while children still point at the old value.
//     That is the third code, and it is the only one that needs a second table.
//   - org_id — a nullable single-column foreign key. Left NULL it satisfies its
//     constraint, and a bare NOT EXISTS over NULL is true: the measurement
//     [[D-042]] is built on.
//   - (region_id, zone) — a composite foreign key with a nullable half. Any NULL
//     column disables the whole constraint, so the guard is every column
//     non-null and not this one.
//   - (tenant_id, slug) — a composite unique key with a nullable half. Under the
//     default NULLS DISTINCT two rows that are NULL there do not collide.
//   - label — the unreproducible key. A partial index on PostgreSQL and SQLite,
//     a prefix key on MySQL and MariaDB, because CREATE UNIQUE INDEX ... WHERE
//     is error 1064 on both of those.
//   - alt — its plain twin, the same shape and reproducible, so an assertion
//     that the hard one is skipped is a control rather than a statement.
//
// Every statement drops before it creates: the acceptance bar is integration
// green twice in a row and this fixture creates tables and indexes.
var pbSchema = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS pb_note`,
		`DROP TABLE IF EXISTS pb_doc`,
		`DROP TABLE IF EXISTS pb_org`,
		`DROP TABLE IF EXISTS pb_region`,
		`CREATE TABLE pb_org (id BIGINT NOT NULL PRIMARY KEY)`,
		`CREATE TABLE pb_region (
			id   BIGINT      NOT NULL,
			zone VARCHAR(8)  NOT NULL,
			PRIMARY KEY (id, zone)
		)`,
		`CREATE TABLE pb_doc (
			id        BIGSERIAL    PRIMARY KEY,
			tenant_id BIGINT       NOT NULL,
			email     VARCHAR(64)  NOT NULL,
			code      VARCHAR(16)  NOT NULL,
			slug      VARCHAR(64)      NULL,
			label     VARCHAR(64)      NULL,
			alt       VARCHAR(64)      NULL,
			archived  INTEGER      NOT NULL DEFAULT 0,
			org_id    BIGINT           NULL,
			region_id BIGINT           NULL,
			zone      VARCHAR(8)       NULL,
			CONSTRAINT pb_doc_email_uk  UNIQUE (email),
			CONSTRAINT pb_doc_code_uk   UNIQUE (code),
			CONSTRAINT pb_doc_slug_uk   UNIQUE (tenant_id, slug),
			CONSTRAINT pb_doc_org_fk    FOREIGN KEY (org_id) REFERENCES pb_org (id),
			CONSTRAINT pb_doc_region_fk FOREIGN KEY (region_id, zone) REFERENCES pb_region (id, zone)
		)`,
		`CREATE UNIQUE INDEX pb_doc_ux_hard ON pb_doc (label) WHERE archived = 0`,
		`CREATE UNIQUE INDEX pb_doc_ux_easy ON pb_doc (alt)`,
		`CREATE TABLE pb_note (
			id       BIGINT      NOT NULL PRIMARY KEY,
			doc_code VARCHAR(16) NOT NULL,
			CONSTRAINT pb_note_code_fk FOREIGN KEY (doc_code) REFERENCES pb_doc (code)
				ON UPDATE NO ACTION ON DELETE NO ACTION
		)`,
	},
	"mysql": {
		`DROP TABLE IF EXISTS pb_note`,
		`DROP TABLE IF EXISTS pb_doc`,
		`DROP TABLE IF EXISTS pb_org`,
		`DROP TABLE IF EXISTS pb_region`,
		`CREATE TABLE pb_org (id BIGINT NOT NULL PRIMARY KEY) ENGINE=InnoDB`,
		`CREATE TABLE pb_region (
			id   BIGINT     NOT NULL,
			zone VARCHAR(8) NOT NULL,
			PRIMARY KEY (id, zone)
		) ENGINE=InnoDB`,
		`CREATE TABLE pb_doc (
			id        BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
			tenant_id BIGINT       NOT NULL,
			email     VARCHAR(64)  NOT NULL,
			code      VARCHAR(16)  NOT NULL,
			slug      VARCHAR(64)      NULL,
			label     VARCHAR(64)      NULL,
			alt       VARCHAR(64)      NULL,
			archived  INT          NOT NULL DEFAULT 0,
			org_id    BIGINT           NULL,
			region_id BIGINT           NULL,
			zone      VARCHAR(8)       NULL,
			CONSTRAINT pb_doc_email_uk  UNIQUE (email),
			CONSTRAINT pb_doc_code_uk   UNIQUE (code),
			CONSTRAINT pb_doc_slug_uk   UNIQUE (tenant_id, slug),
			UNIQUE KEY pb_doc_ux_hard (label(8)),
			UNIQUE KEY pb_doc_ux_easy (alt),
			CONSTRAINT pb_doc_org_fk    FOREIGN KEY (org_id) REFERENCES pb_org (id),
			CONSTRAINT pb_doc_region_fk FOREIGN KEY (region_id, zone) REFERENCES pb_region (id, zone)
		) ENGINE=InnoDB`,
		`CREATE TABLE pb_note (
			id       BIGINT      NOT NULL PRIMARY KEY,
			doc_code VARCHAR(16) NOT NULL,
			CONSTRAINT pb_note_code_fk FOREIGN KEY (doc_code) REFERENCES pb_doc (code)
				ON UPDATE NO ACTION ON DELETE NO ACTION
		) ENGINE=InnoDB`,
	},
	"sqlite": {
		`DROP TABLE IF EXISTS pb_note`,
		`DROP TABLE IF EXISTS pb_doc`,
		`DROP TABLE IF EXISTS pb_org`,
		`DROP TABLE IF EXISTS pb_region`,
		`CREATE TABLE pb_org (id INTEGER NOT NULL PRIMARY KEY)`,
		`CREATE TABLE pb_region (
			id   INTEGER    NOT NULL,
			zone VARCHAR(8) NOT NULL,
			PRIMARY KEY (id, zone)
		)`,
		`CREATE TABLE pb_doc (
			id        INTEGER      PRIMARY KEY,
			tenant_id BIGINT       NOT NULL,
			email     VARCHAR(64)  NOT NULL,
			code      VARCHAR(16)  NOT NULL,
			slug      VARCHAR(64)      NULL,
			label     VARCHAR(64)      NULL,
			alt       VARCHAR(64)      NULL,
			archived  INTEGER      NOT NULL DEFAULT 0,
			org_id    BIGINT           NULL,
			region_id BIGINT           NULL,
			zone      VARCHAR(8)       NULL,
			CONSTRAINT pb_doc_email_uk  UNIQUE (email),
			CONSTRAINT pb_doc_code_uk   UNIQUE (code),
			CONSTRAINT pb_doc_slug_uk   UNIQUE (tenant_id, slug),
			CONSTRAINT pb_doc_org_fk    FOREIGN KEY (org_id) REFERENCES pb_org (id),
			CONSTRAINT pb_doc_region_fk FOREIGN KEY (region_id, zone) REFERENCES pb_region (id, zone)
		)`,
		`CREATE UNIQUE INDEX pb_doc_ux_hard ON pb_doc (label) WHERE archived = 0`,
		`CREATE UNIQUE INDEX pb_doc_ux_easy ON pb_doc (alt)`,
		`CREATE TABLE pb_note (
			id       INTEGER     NOT NULL PRIMARY KEY,
			doc_code VARCHAR(16) NOT NULL,
			CONSTRAINT pb_note_code_fk FOREIGN KEY (doc_code) REFERENCES pb_doc (code)
				ON UPDATE NO ACTION ON DELETE NO ACTION
		)`,
	},
}

// pbBaitLabel is the label the negative twin writes to bait a probe into
// replaying the unreproducible key, per engine.
//
// A partial index has a bait and a prefix key does not, and that is a fact about
// the two rather than a gap in the fixture. Replaying a partial index as full
// equality *invents* a violation, because the stored row it matches is not in
// the index at all — so a bait exists. Replaying a prefix key as full equality
// can only *miss* one, because equal whole values are equal in their first n
// characters too. MySQL and MariaDB therefore write no label here, and the
// assertion that the hard key is never named holds on all four engines
// regardless.
var pbBaitLabel = map[string]string{
	"postgres": "BAIT-LABEL",
	"sqlite":   "BAIT-LABEL",
}

// PbDoc is the model. Every nullable column is an Opt so a test can write an
// explicit NULL, which is what the foreign-key guard is about.
type PbDoc struct {
	ID       int64            `db:"id,pk,auto"`
	TenantID int64            `db:"tenant_id"`
	Email    string           `db:"email"`
	Code     string           `db:"code"`
	Slug     crud.Opt[string] `db:"slug"`
	Label    crud.Opt[string] `db:"label"`
	Alt      crud.Opt[string] `db:"alt"`
	Archived int64            `db:"archived"`
	OrgID    crud.Opt[int64]  `db:"org_id"`
	RegionID crud.Opt[int64]  `db:"region_id"`
	Zone     crud.Opt[string] `db:"zone"`
}

// PbDocUpdate is the partial-update DTO.
type PbDocUpdate struct {
	Email    *string
	Code     *string
	Slug     crud.Opt[string]
	Label    crud.Opt[string]
	Alt      crud.Opt[string]
	Archived *int64
	OrgID    crud.Opt[int64]
	RegionID crud.Opt[int64]
	Zone     crud.Opt[string]
}

var PbDocs = basic.Define[PbDoc, int64, PbDocUpdate]("pb_doc")
