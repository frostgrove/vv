//go:build integration

package integration

// The fixture the catalog tests read.
//
// It is its own schema and not a corner of egSchema, for two reasons. egSchema
// is keyed "postgres"/"mysql" so MySQL and MariaDB share DDL, and these four
// engines disagree about which unique keys they can even express — MariaDB has
// no expression index and neither MySQL nor MariaDB has a partial one. And
// egSchema holds no partial index, no CHECK and no foreign-key actions, which is
// three of the five things a catalog is read for.
//
// Every column carries its own opposite on the column next to it: a NOT NULL
// beside a nullable one, a bounded VARCHAR beside an unbounded text, a generated
// column beside a plain one, a defaulted one beside an undefaulted one. That is
// what makes the assertions controls rather than statements — a loader that
// hardcoded any of these answers fails on the other half of the pair.
//
// The twin is cat_rows_ux_hard and cat_rows_ux_easy: the same shape, one of them
// a unique key this catalog cannot reproduce from a value and one it can. Which
// kind of key that is differs per engine, because CREATE UNIQUE INDEX ... WHERE
// is error 1064 on MySQL 8.4 and MariaDB 11.4 — so it is partial-versus-plain on
// PostgreSQL and SQLite and prefix-versus-full-length on the other two.
//
// The other pairs, each one engine's only shape of its kind:
//
//   - cat_rows_uc_def against cat_rows_uc, PostgreSQL only. A DEFERRABLE key is
//     one the server does not apply until COMMIT, which is exactly the key a
//     pre-flight probe must not claim to have checked. MySQL and MariaDB reject
//     DEFERRABLE on a UNIQUE constraint and SQLite's foreign_key_list exposes no
//     deferrability column at all, which is why §7 says PostgreSQL only.
//   - cat_rows_always_ck against cat_rows_qty_ck, PostgreSQL only. PostgreSQL is
//     the only engine that reports a CHECK's columns, and a CHECK naming none
//     has no key part — an empty name appended there is the same shape an
//     expression key part uses.
//   - cat_ref_code_ux against cat_ref_alt_ux, PostgreSQL only. The first is a
//     bare unique index a foreign key on another table points at; a foreign key
//     carries a conindid too, so an anti-join that stops at conindid deletes it.
//     The second is the same index with nothing pointing at it, so a loader that
//     lost both fails the control instead of passing the pair.
//   - cat_dual, MySQL and MariaDB. One name, two objects: an index name and a
//     foreign-key name live in different namespaces there, so UNIQUE KEY
//     cat_dual beside CONSTRAINT cat_dual FOREIGN KEY is legal and
//     TABLE_CONSTRAINTS answers two rows for it. PostgreSQL's own spelling of
//     the collision is a CHECK and a bare unique index of one name, which is
//     what cat_dual is there. SQLite can express neither — no pragma names a
//     CHECK and none names a foreign key.
//   - cat_rows.short_id, SQLite only. REFERENCES cat_ref with no column list is
//     a foreign key against the parent's primary key, and foreign_key_list
//     answers NULL rather than naming it. MySQL and MariaDB reject the form.
//
// Every statement drops before it creates, because the acceptance bar is
// integration green twice in a row and this fixture creates tables and indexes
// rather than only rows.
var catSchema = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS cat_rows`,
		`DROP TABLE IF EXISTS cat_ref`,
		`CREATE TABLE cat_ref (
			id   BIGINT      NOT NULL PRIMARY KEY,
			code VARCHAR(16) NOT NULL,
			alt  VARCHAR(16) NOT NULL
		)`,
		// Both before cat_rows, because the foreign key below needs the first
		// of them to exist before it can be declared.
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
		// The same name as the CHECK above. A constraint and an index are two
		// namespaces here, so this is legal and the two must stay two.
		`CREATE UNIQUE INDEX cat_dual ON cat_rows (plain)`,
	},
	// MySQL and MariaDB get their own copies rather than one shared entry. They
	// disagree on the expression index, and a shared entry would have to leave
	// it out of both.
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
		// No expression index: CREATE UNIQUE INDEX ... ((lower(slug))) is error
		// 1064 here, which is [[D-019]] difference 9.
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

// The two schemas the search_path test needs, each holding a table of the same
// bare name and a different shape. Two schemas rather than two databases,
// because what is on trial is the connection's own resolution of a bare name.
var catSearchPathSchema = []string{
	`DROP SCHEMA IF EXISTS cat_s1 CASCADE`,
	`DROP SCHEMA IF EXISTS cat_s2 CASCADE`,
	`CREATE SCHEMA cat_s1`,
	`CREATE SCHEMA cat_s2`,
	`CREATE TABLE cat_s1.cat_same (only_here BIGINT NOT NULL PRIMARY KEY)`,
	`CREATE TABLE cat_s2.cat_same (elsewhere TEXT NOT NULL PRIMARY KEY, extra INT NULL)`,
}
