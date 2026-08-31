//go:build integration

package integration

import (
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type User struct {
	ID        int64         `db:"id,pk,auto"`
	TenantID  int64         `db:"tenant_id,immutable"`
	Email     string        `db:"email"`
	Name      string        `db:"name"`
	Age       crud.Opt[int] `db:"age"`
	Active    bool          `db:"active"`
	CreatedAt time.Time     `db:"created_at,generated"`
}

type UserUpdate struct {
	Email  *string
	Name   *string
	Age    crud.Opt[int]
	Active *bool
}

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")

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
