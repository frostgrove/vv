//go:build integration

package integration

var catSchema = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS cat_rows`,
		`DROP TABLE IF EXISTS cat_ref`,
		`CREATE TABLE cat_ref (
			id   BIGINT      NOT NULL PRIMARY KEY,
			code VARCHAR(16) NOT NULL,
			alt  VARCHAR(16) NOT NULL
		)`,

		`CREATE UNIQUE INDEX cat_ref_code_ux ON cat_ref (code)`,
		`CREATE UNIQUE INDEX cat_ref_alt_ux ON cat_ref (alt)`,
		`CREATE TABLE cat_rows (
			id         BIGSERIAL   PRIMARY KEY,
			req        VARCHAR(255) NOT NULL,
			opt        TEXT             NULL,
			qty        INTEGER      NOT NULL DEFAULT 7,
			note       TEXT             NULL,
			gen        TEXT         GENERATED ALWAYS AS (lower(req)) STORED,
			plain      TEXT             NULL,
			slug       VARCHAR(64)      NULL,
			deleted_at TIMESTAMPTZ      NULL,
			del_id     BIGINT           NULL,
			upd_id     BIGINT           NULL,
			ref_code   VARCHAR(16)      NULL,
			CONSTRAINT cat_rows_qty_ck CHECK (qty > 0),
			CONSTRAINT cat_rows_always_ck CHECK (1 = 1),
			CONSTRAINT cat_dual CHECK (qty < 1000),
			CONSTRAINT cat_rows_uc UNIQUE (req, qty),
			CONSTRAINT cat_rows_uc_def UNIQUE (note) DEFERRABLE INITIALLY DEFERRED,
			CONSTRAINT cat_rows_fk_del FOREIGN KEY (del_id) REFERENCES cat_ref (id)
				ON DELETE CASCADE ON UPDATE SET NULL,
			CONSTRAINT cat_rows_fk_upd FOREIGN KEY (upd_id) REFERENCES cat_ref (id)
				ON DELETE SET NULL ON UPDATE CASCADE,
			CONSTRAINT cat_rows_fk_code FOREIGN KEY (ref_code) REFERENCES cat_ref (code)
		)`,
		`CREATE UNIQUE INDEX cat_rows_ux_hard ON cat_rows (slug) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX cat_rows_ux_easy ON cat_rows (slug, qty)`,
		`CREATE UNIQUE INDEX cat_rows_ux_expr ON cat_rows ((lower(slug)))`,

		`CREATE UNIQUE INDEX cat_dual ON cat_rows (plain)`,
	},

	"mysql": {
		`DROP TABLE IF EXISTS cat_rows`,
		`DROP TABLE IF EXISTS cat_ref`,
		`CREATE TABLE cat_ref (
			id   BIGINT      NOT NULL PRIMARY KEY,
			code VARCHAR(16) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE cat_rows (
			id         BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
			req        VARCHAR(255) NOT NULL,
			opt        TEXT             NULL,
			qty        INT          NOT NULL DEFAULT 7,
			note       TEXT             NULL,
			gen        VARCHAR(255) GENERATED ALWAYS AS (lower(req)) STORED,
			plain      VARCHAR(255)     NULL,
			slug       VARCHAR(64)      NULL,
			deleted_at DATETIME(6)      NULL,
			del_id     BIGINT           NULL,
			upd_id     BIGINT           NULL,
			shr_id     BIGINT           NULL,
			CONSTRAINT cat_rows_qty_ck CHECK (qty > 0),
			CONSTRAINT cat_rows_uc UNIQUE (req, qty),
			UNIQUE KEY cat_dual (shr_id),
			CONSTRAINT cat_rows_fk_del FOREIGN KEY (del_id) REFERENCES cat_ref (id)
				ON DELETE CASCADE ON UPDATE SET NULL,
			CONSTRAINT cat_rows_fk_upd FOREIGN KEY (upd_id) REFERENCES cat_ref (id)
				ON DELETE SET NULL ON UPDATE CASCADE,
			CONSTRAINT cat_dual FOREIGN KEY (shr_id) REFERENCES cat_ref (id)
		) ENGINE=InnoDB`,
		`CREATE UNIQUE INDEX cat_rows_ux_hard ON cat_rows (slug(10))`,
		`CREATE UNIQUE INDEX cat_rows_ux_easy ON cat_rows (slug, qty)`,
		`CREATE UNIQUE INDEX cat_rows_ux_expr ON cat_rows ((lower(slug)))`,
	},
	"mariadb": {
		`DROP TABLE IF EXISTS cat_rows`,
		`DROP TABLE IF EXISTS cat_ref`,
		`CREATE TABLE cat_ref (
			id   BIGINT      NOT NULL PRIMARY KEY,
			code VARCHAR(16) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE cat_rows (
			id         BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
			req        VARCHAR(255) NOT NULL,
			opt        TEXT             NULL,
			qty        INT          NOT NULL DEFAULT 7,
			note       TEXT             NULL,
			gen        VARCHAR(255) GENERATED ALWAYS AS (lower(req)) STORED,
			plain      VARCHAR(255)     NULL,
			slug       VARCHAR(64)      NULL,
			deleted_at DATETIME(6)      NULL,
			del_id     BIGINT           NULL,
			upd_id     BIGINT           NULL,
			shr_id     BIGINT           NULL,
			CONSTRAINT cat_rows_qty_ck CHECK (qty > 0),
			CONSTRAINT cat_rows_uc UNIQUE (req, qty),
			UNIQUE KEY cat_dual (shr_id),
			CONSTRAINT cat_rows_fk_del FOREIGN KEY (del_id) REFERENCES cat_ref (id)
				ON DELETE CASCADE ON UPDATE SET NULL,
			CONSTRAINT cat_rows_fk_upd FOREIGN KEY (upd_id) REFERENCES cat_ref (id)
				ON DELETE SET NULL ON UPDATE CASCADE,
			CONSTRAINT cat_dual FOREIGN KEY (shr_id) REFERENCES cat_ref (id)
		) ENGINE=InnoDB`,
		`CREATE UNIQUE INDEX cat_rows_ux_hard ON cat_rows (slug(10))`,
		`CREATE UNIQUE INDEX cat_rows_ux_easy ON cat_rows (slug, qty)`,
	},
	"sqlite": {
		`DROP TABLE IF EXISTS cat_rows`,
		`DROP TABLE IF EXISTS cat_ref`,
		`CREATE TABLE cat_ref (
			id   INTEGER PRIMARY KEY,
			code TEXT    NOT NULL
		)`,
		`CREATE TABLE cat_rows (
			id         INTEGER      PRIMARY KEY,
			req        VARCHAR(255) NOT NULL,
			opt        TEXT             NULL,
			qty        INTEGER      NOT NULL DEFAULT 7,
			note       TEXT             NULL,
			gen        TEXT         GENERATED ALWAYS AS (lower(req)) STORED,
			plain      TEXT             NULL,
			slug       TEXT             NULL,
			deleted_at TEXT             NULL,
			del_id     INTEGER          NULL REFERENCES cat_ref (id)
				ON DELETE CASCADE ON UPDATE SET NULL,
			upd_id     INTEGER          NULL REFERENCES cat_ref (id)
				ON DELETE SET NULL ON UPDATE CASCADE,
			short_id   INTEGER          NULL REFERENCES cat_ref,
			CONSTRAINT cat_rows_qty_ck CHECK (qty > 0),
			CONSTRAINT cat_rows_uc UNIQUE (req, qty)
		)`,
		`CREATE UNIQUE INDEX cat_rows_ux_hard ON cat_rows (slug) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX cat_rows_ux_easy ON cat_rows (slug, qty)`,
		`CREATE UNIQUE INDEX cat_rows_ux_expr ON cat_rows (lower(slug))`,
	},
}

var catSearchPathSchema = []string{
	`DROP SCHEMA IF EXISTS cat_s1 CASCADE`,
	`DROP SCHEMA IF EXISTS cat_s2 CASCADE`,
	`CREATE SCHEMA cat_s1`,
	`CREATE SCHEMA cat_s2`,
	`CREATE TABLE cat_s1.cat_same (only_here BIGINT NOT NULL PRIMARY KEY)`,
	`CREATE TABLE cat_s2.cat_same (elsewhere TEXT NOT NULL PRIMARY KEY, extra INT NULL)`,
}
