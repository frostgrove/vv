package vvdb_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/frostgrove/vv/utils/vvdb"
)

func TestSecretsStayOutOfOrdinaryRendering(t *testing.T) {
	const password = "sentinel-password"
	const token = "sentinel-token"
	const opaqueParam = "sentinel-under-an-unclassified-key"
	config := vvdb.Config{
		Engine:   vvdb.Postgres,
		Host:     "db.internal",
		User:     "orders",
		Password: password,
		Name:     "orders",
		Params: vvdb.Params{
			"application_name": "orders-api",
			"oauth_token":      token,
			"driver_option":    opaqueParam,
		},
		Replica: &vvdb.Config{
			Password: "sentinel-replica-password",
			DSN:      "sentinel-replica-dsn",
			Params:   vvdb.Params{"replica_option": "sentinel-replica-param"},
		},
	}

	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	type applicationConfig struct {
		Database vvdb.Config `json:"database"`
	}
	outer := applicationConfig{Database: config}
	outerJSON, err := json.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("config", "db", config)
	var outerLog bytes.Buffer
	slog.New(slog.NewJSONHandler(&outerLog, nil)).Info("config", "app", outer)
	views := []string{
		fmt.Sprintf("%v", config), fmt.Sprintf("%+v", config), fmt.Sprintf("%#v", config),
		fmt.Sprintf("%d", config), fmt.Sprintf("%s", config), fmt.Sprintf("%q", config), fmt.Sprintf("%x", config),
		fmt.Sprintf("%v", &config), fmt.Sprintf("%d", &config), fmt.Sprintf("%p", &config),
		fmt.Sprintf("%d", config.Password), fmt.Sprintf("%s", config.Password), fmt.Sprintf("%q", config.Password), fmt.Sprintf("%x", config.Password),
		fmt.Sprintf("%d", config.Params), fmt.Sprintf("%s", config.Params), fmt.Sprintf("%q", config.Params), fmt.Sprintf("%x", config.Params),
		fmt.Sprintf("%v", outer), fmt.Sprintf("%+v", outer), fmt.Sprintf("%#v", outer),
		string(encoded), string(outerJSON), log.String(), outerLog.String(),
	}

	for _, value := range []any{config, &config, config.Password, &config.Password, config.Params, &config.Params} {
		for _, verb := range []rune{'v', 's', 'q', 'x', 'X', 'b', 'c', 'd', 'o', 'O', 'e', 'E', 'f', 'F', 'g', 'G', 'U', 't'} {
			views = append(views, fmt.Sprintf("%"+string(verb), value))
		}
	}
	for _, view := range views {
		for _, secret := range []string{password, token, opaqueParam, "sentinel-replica-password", "sentinel-replica-dsn", "sentinel-replica-param"} {
			if strings.Contains(view, secret) {
				t.Fatalf("ordinary rendering exposed %q: %s", secret, view)
			}
		}
	}

	type copiedSecret struct {
		Credential vvdb.Secret `json:"credential"`
	}
	copied, err := json.Marshal(copiedSecret{Credential: vvdb.Secret(password)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(copied), password) {
		t.Fatalf("copied Secret exposed its value: %s", copied)
	}
}

func TestConfigFormattingDoesNotFollowReplicaCycles(t *testing.T) {
	config := vvdb.Config{Engine: vvdb.Postgres, Password: "sentinel-cycle-password"}
	config.Replica = &config
	for _, view := range []string{fmt.Sprintf("%v", config), fmt.Sprintf("%#v", &config), config.String()} {
		if strings.Contains(view, "sentinel-cycle-password") {
			t.Fatalf("cyclic topology exposed its password: %s", view)
		}
		if !strings.Contains(view, "Replica:true") {
			t.Fatalf("cyclic topology lost the support-safe replica marker: %s", view)
		}
	}
}

func TestRedactedDSNKeepsTheTargetAndRemovesCredentials(t *testing.T) {
	for _, config := range []vvdb.Config{
		{
			Engine: vvdb.Postgres, Host: "postgres.internal", User: "orders",
			Password: "sentinel-password", Name: "orders",
			Params: vvdb.Params{"driver_option": "sentinel-token"},
		},
		{
			Engine: vvdb.MySQL, Host: "mysql.internal", User: "orders",
			Password: "sentinel-password", Name: "orders",
			Params: vvdb.Params{"driver_option": "sentinel-token"},
		},
	} {
		got, err := vvdb.RedactedDSN(&config)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "sentinel-password") || strings.Contains(got, "sentinel-token") {
			t.Fatalf("redacted DSN exposed a credential: %s", got)
		}
		if !strings.Contains(got, config.Host) || !strings.Contains(got, config.Name) {
			t.Fatalf("redacted DSN stopped being useful for support: %s", got)
		}
	}
}

func TestRedactedMySQLDSNDoesNotMistakePasswordPunctuationForGrammar(t *testing.T) {
	config := base(vvdb.MySQL)
	config.Password = `prefix?sentinel@word/slash:&=#`
	got, err := vvdb.RedactedDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "sentinel") || strings.Contains(got, "prefix?") {
		t.Fatalf("redacted MySQL DSN leaked punctuation-bearing password: %s", got)
	}
	if !strings.Contains(got, config.Host) || !strings.Contains(got, config.Name) {
		t.Fatalf("redacted MySQL DSN lost its target: %s", got)
	}
}

func TestTypedServerConnectionsUseVerifiedTLSByDefault(t *testing.T) {
	postgres := base(vvdb.Postgres)
	got, err := vvdb.PostgresDSN(&postgres)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if mode := u.Query().Get("sslmode"); mode != "verify-full" {
		t.Fatalf("postgres default sslmode = %q, want verify-full", mode)
	}

	mysql := base(vvdb.MySQL)
	got, err = vvdb.MySQLDSN(&mysql)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "tls=true") {
		t.Fatalf("mysql default is not verified TLS: %s", got)
	}

	postgres.Host = "localhost"
	postgres.SSLMode = "disable"
	got, err = vvdb.PostgresDSN(&postgres)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(got)
	if mode := u.Query().Get("sslmode"); mode != "disable" {
		t.Fatalf("explicit local plaintext waiver was not preserved: %q", mode)
	}
}

func TestAnUnparseableRawDSNIsHiddenRatherThanGuessedAt(t *testing.T) {
	config := vvdb.Config{
		Engine: vvdb.Postgres,
		DSN:    "host=db.internal user=orders password=sentinel-password dbname=orders",
	}
	got, err := vvdb.RedactedDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "sentinel-password") {
		t.Fatalf("unknown raw DSN grammar was echoed: %s", got)
	}
}

func TestRedactedRawDSNFailsClosedOnUnknownGrammarAndEveryQueryValue(t *testing.T) {
	for name, config := range map[string]vvdb.Config{
		"opaque postgres": {
			Engine: vvdb.Postgres, DSN: "postgres:sentinel-password",
		},
		"wrong postgres scheme": {
			Engine: vvdb.Postgres, DSN: "mysql://user:sentinel-password@db.internal/orders",
		},
		"postgres query and fragment": {
			Engine: vvdb.Postgres, DSN: "postgres://user:sentinel-password@db.internal/orders?sslmode=sentinel-query#sentinel-fragment",
		},
		"mysql query": {
			Engine: vvdb.MySQL, DSN: "user:sentinel-password@tcp(db.internal:3306)/orders?tls=sentinel-query",
		},
		"mysql wrong grammar without userinfo": {
			Engine: vvdb.MySQL, DSN: "postgres://sentinel-password/orders",
		},
		"mysql wrong grammar with userinfo": {
			Engine: vvdb.MariaDB, DSN: "postgres://user:sentinel-password@db.internal/orders",
		},
		"mysql invalid database escape": {
			Engine: vvdb.MySQL, DSN: "tcp(db.internal:3306)/orders%sentinel-password",
		},
		"mysql transport without required address": {
			Engine: vvdb.MySQL, DSN: "tcp4/orders-sentinel-password",
		},
		"sqlite query and fragment": {
			Engine: vvdb.SQLite, DSN: "file:/tmp/orders.db?mode=sentinel-query#sentinel-fragment",
		},
		"sqlite userinfo": {
			Engine: vvdb.SQLite, DSN: "file://user:sentinel-password@localhost/tmp/orders.db",
		},
		"sqlite invalid authority": {
			Engine: vvdb.SQLite, DSN: "file://sentinel-password/tmp/orders.db",
		},
		"sqlite wrong scheme": {
			Engine: vvdb.SQLite, DSN: "postgres://user:sentinel-password@localhost/tmp/orders.db",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := vvdb.RedactedDSN(&config)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, "sentinel") || strings.ContainsAny(got, "?#") {
				t.Fatalf("RedactedDSN() retained raw secret syntax: %q", got)
			}
			if (name == "sqlite userinfo" || name == "sqlite invalid authority" ||
				name == "mysql invalid database escape" || name == "mysql transport without required address") && got != "[REDACTED]" {
				t.Fatalf("invalid raw DSN grammar was treated as a support target: %q", got)
			}
		})
	}
}

func TestRedactedMySQLDSNRemovesCredentialsBeforeCheckingTheTransport(t *testing.T) {
	config := vvdb.Config{
		Engine: vvdb.MySQL,
		DSN:    "orders:prefix://sentinel-password@tcp(db.internal:3306)/orders",
	}
	got, err := vvdb.RedactedDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	if got != "tcp(db.internal:3306)/orders" {
		t.Fatalf("valid punctuation-bearing credentials lost the support target: %q", got)
	}
}

func TestRedactedTypedSocketAndRelativeSQLiteTargetsStayUseful(t *testing.T) {
	postgres := base(vvdb.Postgres)
	postgres.Host = "/var/run/postgresql"
	postgres.SSLMode = "disable"
	got, err := vvdb.RedactedDSN(&postgres)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, postgres.Host) || !strings.Contains(got, postgres.Name) {
		t.Fatalf("typed socket target lost support context: %q", got)
	}

	sqlite := vvdb.Config{Engine: vvdb.SQLite, Path: "data/orders.db", Params: vvdb.Params{"mode": "sentinel-query"}}
	got, err = vvdb.RedactedDSN(&sqlite)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "data/orders.db") || strings.Contains(got, "sentinel") || strings.ContainsAny(got, "?#") {
		t.Fatalf("relative SQLite target is not support-safe: %q", got)
	}
}

func TestRedactErrorHidesDisplayAndPreservesCause(t *testing.T) {
	cause := errors.New("sentinel-parser-detail")
	err := vvdb.RedactError("parser rejected configuration", cause)
	views := []string{
		err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err),
		fmt.Sprintf("%#v", err), fmt.Sprintf("%s", err), fmt.Sprintf("%q", err),
		fmt.Sprintf("%x", err), fmt.Sprintf("%d", err), fmt.Errorf("outer: %w", err).Error(),
		fmt.Sprintf("%#v", fmt.Errorf("outer: %w", err)),
	}
	for _, view := range views {
		if strings.Contains(view, "sentinel") {
			t.Fatalf("RedactError exposed its cause: %s", view)
		}
	}
	if !errors.Is(err, cause) {
		t.Fatal("RedactError stopped errors.Is from reaching the cause")
	}
	if vvdb.RedactError("unused", nil) != nil {
		t.Fatal("RedactError(nil) must stay nil")
	}
}
