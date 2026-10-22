package config

import (
	"context"
	"testing"
)

func TestSelfHostDatabaseDefaults(t *testing.T) {
	t.Setenv("AWS_CONFIG_ENABLED", "false")
	t.Setenv("DB_MIGRATIONS_ON_START", "")
	t.Setenv("PRIMARY_DB_DIRECT", "")
	t.Setenv("PRIMARY_DB", "postgres://self-host.example/warmbly")
	cfg, err := NewConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AWSConfigEnabled || cfg.params != nil || cfg.secrets != nil {
		t.Fatal("self-host defaults must not initialize AWS clients")
	}
	if !cfg.LoadMigrationsOnStart(context.Background()) {
		t.Fatal("self-host upgrades must continue applying migrations on startup")
	}
	if cfg.LoadPrimaryDBDirectEndpoint(context.Background()) != "" {
		t.Fatal("a separate direct connection must remain optional")
	}
	endpoint, err := cfg.LoadPrimaryDBEndpoint(context.Background())
	if err != nil || endpoint != "postgres://self-host.example/warmbly" {
		t.Fatal("existing self-host database configuration must remain usable")
	}
}

func TestDeploymentMigrationJobIsOptIn(t *testing.T) {
	t.Setenv("AWS_CONFIG_ENABLED", "false")
	t.Setenv("DB_MIGRATIONS_ON_START", "false")
	cfg, err := NewConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LoadMigrationsOnStart(context.Background()) {
		t.Fatal("explicit deployment-managed migrations setting was not honored")
	}
}
