package seed

import (
	"fmt"
	"os"
	"strings"
)

// DevEnvironment refuses unless APP_ENV names local development explicitly.
// The seeders plant accounts with published credentials, so an unset APP_ENV
// refuses too.
func DevEnvironment() error {
	const fix = "it creates accounts with published passwords and API keys, so it runs only against a local development database. " +
		"Set APP_ENV=dev (or development, local) to run it there, and never run it against a production database"
	switch env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))); env {
	case "dev", "development", "local":
		return nil
	case "":
		return fmt.Errorf("APP_ENV is not set: %s", fix)
	default:
		return fmt.Errorf("APP_ENV=%s: %s", env, fix)
	}
}
