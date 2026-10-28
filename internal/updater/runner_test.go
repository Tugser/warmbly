package updater

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCommandArgvTakesAnAbsoluteExecutableAndPlainArguments(t *testing.T) {
	got, err := commandArgv("  /opt/warmbly/src/scripts/upgrade-bare-metal.sh --env=/etc/warmbly/.env ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/warmbly/src/scripts/upgrade-bare-metal.sh", "--env=/etc/warmbly/.env"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}

	for _, raw := range []string{
		"",
		"upgrade.sh",
		"./upgrade.sh",
		"/bin/upgrade; rm -rf /",
		"/bin/upgrade && reboot",
		"/bin/upgrade $(id)",
		"/bin/upgrade `id`",
		"/bin/upgrade | tee x",
		"/bin/upgrade > /tmp/x",
		"/bin/upgrade 'quoted'",
	} {
		if _, err := commandArgv(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

func TestNewRunnerValidatesOperatorSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := Config{Mode: ModeCommand, RepoDir: dir, StateDir: t.TempDir(), Command: "/usr/local/bin/upgrade"}
	if _, err := NewRunner(base); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"shell command":   func(c *Config) { c.Command = "cd /src && make up" },
		"compose project": func(c *Config) { c.ComposeProject = "warmbly;id" },
		"compose profile": func(c *Config) { c.ComposeProfiles = "updater,--x" },
		"remote":          func(c *Config) { c.Remote = "--upload-pack=x" },
	} {
		cfg := base
		mutate(&cfg)
		if _, err := NewRunner(cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestComposeNamesAreValidated(t *testing.T) {
	if !composeProjectRe.MatchString("warmbly") || composeProjectRe.MatchString("warmbly;id") || composeProjectRe.MatchString("-p") {
		t.Fatal("compose project pattern")
	}
	if !composeProfileRe.MatchString("updater") || composeProfileRe.MatchString("--x") || composeProfileRe.MatchString("a b") {
		t.Fatal("compose profile pattern")
	}
}

func TestSelfRecreatePassesComposeArgumentsPositionally(t *testing.T) {
	r := &Runner{cfg: Config{ComposeProject: "warmbly", ComposeProfiles: "updater"}}
	got := r.selfRecreateArgs()
	want := []string{
		"compose", "-p", "warmbly", "--profile", "updater",
		"run", "-d", "--rm", "--no-deps", "--entrypoint", "sh", selfService, "-c", `sleep 3; exec docker "$@"`, "sh",
		"compose", "-p", "warmbly", "--profile", "updater", "up", "-d", "--no-build", "--no-deps", selfService,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q\nwant   %q", got, want)
	}
}
