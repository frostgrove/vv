//go:build integration

// Package integration runs one conformance suite against every supported
// driver and both databases. The suite only ever talks to a crud.Source, so
// adding a driver means adding a target — never a test.
package integration

import (
	"time"

	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/repo/basic"
	"github.com/shardit-io/go-rx-crud/repo/decorators/specs"
)

// User is the model under test. It exercises every mapping feature at once: a
// database-generated key, an immutable column, a nullable column through Opt
// and a column the database computes.
type User struct {
	ID        int64         `db:"id,pk,auto"`
	TenantID  int64         `db:"tenant_id,immutable"`
	Email     string        `db:"email"`
	Name      string        `db:"name"`
	Age       crud.Opt[int] `db:"age"`
	Active    bool          `db:"active"`
	CreatedAt time.Time     `db:"created_at,generated"`
}

// UserUpdate is the partial-update DTO: pointers for optional non-nullable
// fields, Opt for the nullable one.
type UserUpdate struct {
	Email  *string
	Name   *string
	Age    crud.Opt[int]
	Active *bool
}

// Users is declared once and bound to whichever datasource a target provides.
var Users = basic.Define[User, int64, UserUpdate]("users")

// The metamodel, validated against User at package initialisation.
type userAttrs struct {
	ID        specs.Ord[User, int64]
	TenantID  specs.Ord[User, int64]
	Email     specs.Str[User]
	Name      specs.Str[User]
	Age       specs.Ord[User, int]
	Active    specs.Attr[User, bool]
	CreatedAt specs.Cmp[User, time.Time]
}

var User_ = specs.Metamodel[User, userAttrs]()

const schemaPostgres = `
DROP TABLE IF EXISTS users;
CREATE TABLE users (
	id         BIGSERIAL PRIMARY KEY,
	tenant_id  BIGINT       NOT NULL,
	email      VARCHAR(255) NOT NULL,
	name       VARCHAR(255) NOT NULL,
	age        INTEGER          NULL,
	active     BOOLEAN      NOT NULL DEFAULT TRUE,
	created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);`

const schemaMySQL = `
CREATE TABLE users (
	id         BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
	tenant_id  BIGINT       NOT NULL,
	email      VARCHAR(255) NOT NULL,
	name       VARCHAR(255) NOT NULL,
	age        INT              NULL,
	active     BOOLEAN      NOT NULL DEFAULT TRUE,
	created_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB;`

// INTEGER PRIMARY KEY is SQLite's rowid alias, which is what makes `auto` work;
// the declared TIMESTAMP type is what tells the driver to hand back a
// time.Time rather than the string the column really holds.
const schemaSQLite = `
CREATE TABLE users (
	id         INTEGER   NOT NULL PRIMARY KEY,
	tenant_id  INTEGER   NOT NULL,
	email      TEXT      NOT NULL,
	name       TEXT      NOT NULL,
	age        INTEGER       NULL,
	active     BOOLEAN   NOT NULL DEFAULT TRUE,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
