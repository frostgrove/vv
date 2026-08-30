package dbpgx

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReadWriteOptionsKeepCredentialsOnTheirDeclaredSide(t *testing.T) {
	common := func(pc *pgxpool.Config) {
		pc.ConnConfig.RuntimeParams["application_name"] = "vv"
		pc.ConnConfig.User = "common"
	}
	writer := func(pc *pgxpool.Config) { pc.ConnConfig.User = "writer" }
	reader := func(pc *pgxpool.Config) { pc.ConnConfig.User = "reader" }

	primaryOptions, replicaOptions := splitReadWriteOptions(
		Replica(reader),
		Common(common),
		Primary(writer),
	)
	primary, err := pgxpool.ParseConfig("postgres://base:secret@db/app")
	if err != nil {
		t.Fatal(err)
	}
	replica, err := pgxpool.ParseConfig("postgres://base:secret@replica/app")
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range primaryOptions {
		option(primary)
	}
	for _, option := range replicaOptions {
		option(replica)
	}

	if primary.ConnConfig.User != "writer" || replica.ConnConfig.User != "reader" {
		t.Fatalf("side-specific options did not override Common: primary:%q replica:%q",
			primary.ConnConfig.User, replica.ConnConfig.User)
	}
	if primary.ConnConfig.RuntimeParams["application_name"] != "vv" ||
		replica.ConnConfig.RuntimeParams["application_name"] != "vv" {
		t.Fatal("common option did not reach both configurations")
	}
}

func TestReadWriteOptionConstructorsSnapshotTheirSlices(t *testing.T) {
	first := func(pc *pgxpool.Config) { pc.ConnConfig.User = "first" }
	second := func(pc *pgxpool.Config) { pc.ConnConfig.User = "mutated" }
	caller := []Option{first}
	declared := Primary(caller...)
	caller[0] = second

	primaryOptions, _ := splitReadWriteOptions(declared)
	primary, err := pgxpool.ParseConfig("postgres://base:secret@db/app")
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range primaryOptions {
		option(primary)
	}
	if primary.ConnConfig.User != "first" {
		t.Fatalf("declaration retained caller-owned option slice: %q", primary.ConnConfig.User)
	}
}
