package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/app/keystatus"
)

func runKeys(ctx context.Context, args []string) error {
	if len(args) == 0 {
		keysUsage(os.Stderr)
		return errors.New("`keys` needs a subcommand. Pick one from the list above.")
	}
	switch args[0] {
	case "help", "-h", "--help":
		keysUsage(os.Stdout)
		return nil
	case "status":
		return runKeysScan(ctx, "keys status", args[1:], false)
	case "reseal":
		return runKeysScan(ctx, "keys reseal", args[1:], true)
	case "generate":
		return runKeysGenerate(args[1:])
	}
	keysUsage(os.Stderr)
	return fmt.Errorf("unknown subcommand `keys %s`. Pick one from the list above.", args[0])
}

func keysUsage(w *os.File) {
	fmt.Fprint(w, "Rotate the instance-wide encryption keys without losing what they seal.\n\nUsage:\n  warmblyctl keys <subcommand> [flags]\n\nSubcommands:\n")
	for _, c := range commands {
		if !strings.HasPrefix(c.name, "keys ") {
			continue
		}
		fmt.Fprintf(w, "  %-28s %s\n    %s\n", strings.TrimPrefix(c.name, "keys "), c.summary, c.example)
	}
}

func runKeysScan(ctx context.Context, name string, args []string, reseal bool) error {
	fs := newFlagSet(name)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	var apply, requirePrimary *bool
	var batch *int
	if !reseal {
		requirePrimary = fs.Bool("require-primary", false, "also exit non-zero while any value opens only under a read key")
	}
	if reseal {
		apply = fs.Bool("apply", false, "write the resealed values; without it nothing is written")
		batch = fs.Int("batch", 500, "rows read per query")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := noExtraArgs(fs); err != nil {
		return err
	}
	size := 500
	if batch != nil {
		if *batch < 1 || *batch > 10000 {
			return errors.New("--batch must be between 1 and 10000")
		}
		size = *batch
	}
	write := apply != nil && *apply

	keys, err := keystatus.LoadKeyring(ctx)
	if err != nil {
		return err
	}
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	report, err := keystatus.Scan(ctx, c.db.Pool, keys, keystatus.Options{Batch: size, Write: write})
	if err != nil {
		return err
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		printKeyReport(report, reseal)
	}
	if report.Unreadable() > 0 {
		return keystatus.ErrUnreadable
	}
	if requirePrimary != nil && *requirePrimary && report.OnReadKey() > 0 {
		return keystatus.ErrOnReadKey
	}
	return nil
}

func printKeyReport(r keystatus.Report, reseal bool) {
	if reseal && !r.Applied {
		fmt.Print("Dry run: nothing will be written. Pass --apply to reseal.\n\n")
	}
	fmt.Printf("Instance credential key: primary plus %s.\n", counted(r.InstanceReadKeys, "read key", "read keys"))
	if r.MFAConfigured {
		fmt.Printf("MFA key: primary plus %s.\n", counted(r.MFAReadKeys, "read key", "read keys"))
	} else {
		fmt.Println("MFA key: not configured here, so TOTP enrollments are not checked.")
	}
	fmt.Printf("\n  %-44s %8s %9s %10s %9s %11s\n", "column", "primary", "read key", "workspace", "unsealed", "unreadable")
	var onRead, unreadable, resealed, raced int
	cols := append([]keystatus.Count(nil), r.Columns...)
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Domain < cols[j].Domain })
	for _, c := range cols {
		label := c.Table + "." + c.Column
		if c.Missing {
			fmt.Printf("  %-44s %s\n", label, "table not present")
			continue
		}
		fmt.Printf("  %-44s %8d %9d %10d %9d %11d\n", label, c.Primary, c.ReadKey, c.Workspace, c.Unsealed, c.Unreadable)
		onRead += c.ReadKey
		unreadable += c.Unreadable
		resealed += c.Resealed
		raced += c.Raced
	}
	for _, w := range r.Windows {
		state := "closed"
		if w.Open {
			state = "open until " + w.Until.UTC().Format(time.RFC3339)
		}
		fmt.Printf("\n%s: %s, %s", w.Name, state, counted(w.Entries, "previous value", "previous values"))
	}
	if len(r.Windows) > 0 {
		fmt.Println()
	}
	fmt.Println()
	switch {
	case r.Applied:
		fmt.Printf("Resealed %s under the primary key.", counted(resealed, "value", "values"))
		if raced > 0 {
			fmt.Printf(" %s changed while this ran; run it again to finish them.", counted(raced, "value", "values"))
		}
		fmt.Println()
	case onRead > 0:
		fmt.Printf("%s still open only under a read key. `warmblyctl keys reseal --apply` moves them to the primary.\n", counted(onRead, "value is", "values are"))
	default:
		fmt.Println("Every sealed value opens under the primary key. Retire a read key only once no rollback target, pending job or retained backup still needs it.")
	}
	if unreadable > 0 {
		fmt.Printf("%s under no configured key. They were left untouched: restore the key that sealed them as a read key before retiring anything.\n", counted(unreadable, "value opens", "values open"))
	}
}

func counted(n int, one, many string) string {
	return fmt.Sprintf("%d %s", n, plural(n, one, many))
}

// generatedKeys are the instance-wide values `keys generate` can mint, with their byte length.
var generatedKeys = []struct {
	name  string
	bytes int
}{
	{"CREDENTIALS_ENCRYPTION_KEY", 32},
	{"AUTH_SECRET", 32},
	{"TWOFA_SECRET", 32},
	{"UNSUBSCRIBE_SIGNING_SECRET", 32},
	{"DOMAIN_PROOF_SECRET", 32},
	{"INTERNAL_API_TOKEN", 32},
	{"NODE_BROKER_TOKEN", 32},
	{"SECRET_KEY_BASE", 64},
}

func runKeysGenerate(args []string) error {
	fs := newFlagSet("keys generate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, name := range fs.Args() {
		want[strings.ToUpper(name)] = true
	}
	known := map[string]bool{}
	for _, k := range generatedKeys {
		known[k.name] = true
	}
	for name := range want {
		if !known[name] {
			return fmt.Errorf("%s is not a key this command generates", name)
		}
	}
	for _, k := range generatedKeys {
		if len(want) > 0 && !want[k.name] {
			continue
		}
		buf := make([]byte, k.bytes)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		fmt.Printf("%s=%s\n", k.name, hex.EncodeToString(buf))
	}
	return nil
}
