//go:build integration

package integration

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

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

var pbBaitLabel = map[string]string{
	"postgres": "BAIT-LABEL",
	"sqlite":   "BAIT-LABEL",
}

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

var PbDocs = sqlrepo.Define[PbDoc, int64, PbDocUpdate]("pb_doc")
