//go:build integration

package integration

import "github.com/shardit-io/vv/crud/sqlrepo"

// The provider matrix needs one relation it can grow past a single preload
// batch. It gets its own pair of tables rather than the blog ones because every
// other test reseeds those from scratch, and a thousand rows would make each of
// them slower for no reason.

type MxOwner struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`

	Items []MxItem `rel:"has_many,fk=OwnerID"`
}

type MxItem struct {
	ID      int64  `db:"id,pk,auto"`
	OwnerID int64  `db:"owner_id"`
	Label   string `db:"label"`

	Owner *MxOwner `rel:"belongs_to"`
}

var (
	MxOwners = sqlrepo.Define[MxOwner, int64, struct{}]("mx_owners")
	MxItems  = sqlrepo.Define[MxItem, int64, struct{}]("mx_items")
)

// Both lists start with DROP so a rerun is a clean slate, and both keep the
// generated key so the model's `auto` tag stays honest even though the fixture
// assigns ids itself.
var schemaMatrixPostgres = []string{
	`DROP TABLE IF EXISTS mx_items`,
	`DROP TABLE IF EXISTS mx_owners`,
	`CREATE TABLE mx_owners (
		id   BIGSERIAL    PRIMARY KEY,
		name VARCHAR(255) NOT NULL
	)`,
	`CREATE TABLE mx_items (
		id       BIGSERIAL    PRIMARY KEY,
		owner_id BIGINT       NOT NULL,
		label    VARCHAR(255) NOT NULL
	)`,
	`CREATE INDEX mx_items_owner_id ON mx_items (owner_id)`,
}

var schemaMatrixMySQL = []string{
	`DROP TABLE IF EXISTS mx_items`,
	`DROP TABLE IF EXISTS mx_owners`,
	`CREATE TABLE mx_owners (
		id   BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
		name VARCHAR(255) NOT NULL
	) ENGINE=InnoDB`,
	`CREATE TABLE mx_items (
		id       BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
		owner_id BIGINT       NOT NULL,
		label    VARCHAR(255) NOT NULL,
		INDEX mx_items_owner_id (owner_id)
	) ENGINE=InnoDB`,
}
