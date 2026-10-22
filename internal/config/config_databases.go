package config

import "context"

func (c *Config) LoadGeoDBPath(ctx context.Context) (string, error) {
	return c.GetString(ctx, "GEODB_PATH", "geodb_path")
}

// LoadGeoDBURL is where the database at GEODB_PATH is fetched from when that
// path holds no file yet. Read as a secret because MaxMind's permalink carries
// the account's licence key in its query string.
func (c *Config) LoadGeoDBURL(ctx context.Context) string {
	return c.GetSecretOptional(ctx, "GEODB_URL", "geodb_url", "")
}

func (c *Config) LoadPrimaryDBEndpoint(ctx context.Context) (string, error) {
	return c.GetSecret(ctx, "PRIMARY_DB", "postgres/primary")
}

func (c *Config) LoadPrimaryDBDirectEndpoint(ctx context.Context) string {
	return c.GetSecretOptional(ctx, "PRIMARY_DB_DIRECT", "postgres/primary_direct", "")
}

func (c *Config) LoadMigrationsOnStart(ctx context.Context) bool {
	return c.GetBoolOptional(ctx, "DB_MIGRATIONS_ON_START", "postgres/migrations_on_start", true)
}

func (c *Config) LoadPrimaryRedisEndpoint(ctx context.Context) (string, error) {
	return c.GetSecret(ctx, "REDIS", "redis/primary")
}

func (c *Config) LoadKafkaClusterEndpoint(ctx context.Context) (string, error) {
	return c.GetSecret(ctx, "KAFKA_CLUSTER", "kafka_cluster")
}
