package vvdb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/vv/utils/vvdb"
)

func primary() vvdb.Config {
	return vvdb.Config{
		Engine: vvdb.Postgres, Host: "primary.internal", Port: 5432,
		User: "vv", Password: "s3cret", Name: "app", SSLMode: "require",
		Params: map[string]string{"application_name": "orders"},
		Pool:   vvdb.Pool{MaxOpen: 20},
	}
}

func TestAReplicaInheritsEverythingItDoesNotRestate(t *testing.T) {
	cfg := primary()
	cfg.Replica = &vvdb.Config{Host: "replica.internal"}

	r, ok := cfg.ReadReplica()
	if !ok {
		t.Fatal("a declared replica should be returned")
	}
	if r.Host != "replica.internal" {
		t.Errorf("the replica should keep its own host, got %q", r.Host)
	}
	// The credentials are the failure this inheritance exists to prevent: two
	// copies drift the day one of them is rotated.
	if r.User != "vv" || r.Password != "s3cret" || r.Name != "app" || r.SSLMode != "require" {
		t.Errorf("the replica should inherit what it did not restate, got %+v", r)
	}
	if r.Params["application_name"] != "orders" {
		t.Errorf("params should be inherited too, got %v", r.Params)
	}
	if r.Replica != nil {
		t.Error("a replica of a replica is not a thing this describes")
	}
}

func TestAReplicaOverridesRatherThanMerges(t *testing.T) {
	cfg := primary()
	cfg.Replica = &vvdb.Config{Host: "replica.internal", User: "readonly", Params: map[string]string{"application_name": "orders-ro"}}

	r, _ := cfg.ReadReplica()
	if r.User != "readonly" {
		t.Errorf("a field the replica states is the replica's, got %q", r.User)
	}
	if r.Password != "s3cret" {
		t.Errorf("a field it does not state is still inherited, got %q", r.Password)
	}
	if r.Params["application_name"] != "orders-ro" {
		t.Errorf("a param the replica states wins, got %v", r.Params)
	}
	if cfg.Params["application_name"] != "orders" {
		t.Error("merging must not write into the primary's own map")
	}
}

func TestAReplicaGivenAWholeDSNInheritsNothing(t *testing.T) {
	cfg := primary()
	cfg.Replica = &vvdb.Config{DSN: "postgres://ro:pw@replica.internal:5432/app"}

	r, _ := cfg.ReadReplica()
	if r.Host != "" || r.User != "" {
		t.Errorf("a finished string cannot be merged into; the fields would only contradict it, got %+v", r)
	}
	if r.Engine != vvdb.Postgres {
		t.Errorf("the engine is still needed to pick a driver, got %q", r.Engine)
	}
	got, err := vvdb.DSN(r)
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg.Replica.DSN {
		t.Errorf("the replica string should be used as given, got %s", got)
	}
}

func TestNoReplicaIsNotAnEmptyReplica(t *testing.T) {
	if _, ok := primary().ReadReplica(); ok {
		t.Fatal("a config with no replica must not answer with a usable one — opening it would be a second connection to the primary")
	}
}

func TestAReplicaOfAnotherEngineIsRefused(t *testing.T) {
	cfg := primary()
	cfg.Replica = &vvdb.Config{Engine: vvdb.MySQL, Host: "replica.internal"}
	if err := cfg.Validate(); !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("the two would generate different SQL for the same repository; got %v", err)
	}
}

func TestAReplicaIsValidatedAsItWillBeOpened(t *testing.T) {
	// The control case: the replica fragment on its own has no database name,
	// and validating the fragment rather than the merge would call it invalid.
	cfg := primary()
	cfg.Replica = &vvdb.Config{Host: "replica.internal"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a replica that inherits its name is valid: %v", err)
	}

	cfg.Replica = &vvdb.Config{Host: "replica.internal", SSLMode: "nonsense"}
	if err := cfg.Validate(); err == nil {
		t.Error("a replica with an impossible setting should stop start-up like any other")
	} else if !strings.Contains(err.Error(), "replica") {
		t.Errorf("the message should say which of the two servers is wrong: %v", err)
	}
}

func TestValidateNamesTheFieldThatIsWrong(t *testing.T) {
	for _, tc := range []struct {
		cfg  vvdb.Config
		says string
	}{
		{vvdb.Config{Engine: vvdb.Postgres, Host: "h"}, "name"},
		{vvdb.Config{Engine: vvdb.SQLite}, "path"},
		{vvdb.Config{Engine: vvdb.Postgres, Name: "app", DSN: "postgres://x/y", Host: "h"}, "host"},
	} {
		err := tc.cfg.Validate()
		if err == nil {
			t.Fatalf("%+v was accepted", tc.cfg)
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("the message should name %q so the operator knows which line to edit: %v", tc.says, err)
		}
	}
}

func TestDriverNameDefaultsPerEngineAndIsOverridable(t *testing.T) {
	for _, tc := range []struct {
		cfg  vvdb.Config
		want string
	}{
		{vvdb.Config{Engine: vvdb.Postgres}, "pgx"},
		{vvdb.Config{Engine: vvdb.MySQL}, "mysql"},
		{vvdb.Config{Engine: vvdb.MariaDB}, "mysql"},
		{vvdb.Config{Engine: vvdb.SQLite}, "sqlite"},
		{vvdb.Config{Engine: vvdb.Postgres, Driver: "postgres"}, "postgres"},
	} {
		if got := vvdb.DriverName(tc.cfg); got != tc.want {
			t.Errorf("%s with driver %q should open with %q, got %q", tc.cfg.Engine, tc.cfg.Driver, tc.want, got)
		}
	}
}
