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

	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/app/orgtransfer"
	"github.com/warmbly/warmbly/internal/app/twofa"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
	"github.com/warmbly/warmbly/internal/pkg/secretset"
)

// A rotation ships the new primary with the old key as a read key, reseals, and only then retires the old key.

// keyDomain names which configured key seals a column.
type keyDomain string

const (
	domainInstance keyDomain = "instance"
	domainMFA      keyDomain = "mfa"
)

// sealedColumn is one column holding values sealed under an instance-wide key.
type sealedColumn struct {
	Table  string    `json:"table"`
	Column string    `json:"column"`
	Domain keyDomain `json:"domain"`
	guard  string
}

// instanceOnlyColumns are sealed under an instance-wide key on tables that never travel in a workspace archive.
var instanceOnlyColumns = []sealedColumn{
	{Table: "cloud_link", Column: "token", Domain: domainInstance},
	{Table: "mailbox_erasures", Column: "refresh_token", Domain: domainInstance},
	{Table: "user_totp_settings", Column: "totp_secret_sealed", Domain: domainMFA},
}

// sealedColumns is every column the instance keys seal: the archive registry's instance-key secrets plus the instance-only ones.
func sealedColumns() []sealedColumn {
	var out []sealedColumn
	for _, t := range orgtransfer.Tables {
		for _, s := range t.Secrets {
			if s.Domain == orgtransfer.KeyDomainInstance {
				out = append(out, sealedColumn{Table: t.Name, Column: s.Column, Domain: domainInstance, guard: s.Guard})
			}
		}
	}
	return append(out, instanceOnlyColumns...)
}

// columnCount is where one column's values stand against the configured keys.
type columnCount struct {
	sealedColumn
	Primary    int  `json:"primary"`
	ReadKey    int  `json:"read_key"`
	Unsealed   int  `json:"unsealed"`
	Unreadable int  `json:"unreadable"`
	Resealed   int  `json:"resealed,omitempty"`
	Raced      int  `json:"changed_meanwhile,omitempty"`
	Missing    bool `json:"missing,omitempty"`
}

// keyReport is the whole answer, shaped for --json as much as for a terminal.
type keyReport struct {
	Applied          bool          `json:"applied"`
	InstanceReadKeys int           `json:"instance_read_keys"`
	MFAReadKeys      int           `json:"mfa_read_keys"`
	MFAConfigured    bool          `json:"mfa_configured"`
	Columns          []columnCount `json:"columns"`
	Windows          []graceWindow `json:"grace_windows"`
}

// graceWindow is one previous-credential acceptance window and whether it is still open.
type graceWindow struct {
	Name    string    `json:"name"`
	Until   time.Time `json:"until"`
	Open    bool      `json:"open"`
	Entries int       `json:"entries"`
}

// keyring is the configured keys the backend opens and seals with.
type keyring struct {
	instance     *encrypt.Encrypter
	instanceRead int
	mfa          *[32]byte
	mfaRead      [][32]byte
}

var errUnreadableValues = errors.New("some stored values do not open under any configured key")

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
	var apply *bool
	var batch *int
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

	keys, err := loadKeyring(ctx)
	if err != nil {
		return err
	}
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	report := keyReport{Applied: write, InstanceReadKeys: keys.instanceRead, MFAReadKeys: len(keys.mfaRead), MFAConfigured: keys.mfa != nil, Windows: graceWindows(time.Now())}
	for _, col := range sealedColumns() {
		if col.Domain == domainMFA && keys.mfa == nil {
			continue
		}
		count, err := scanColumn(ctx, c, keys, col, size, write)
		if err != nil {
			return err
		}
		report.Columns = append(report.Columns, count)
	}

	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		printKeyReport(report, reseal)
	}
	for _, col := range report.Columns {
		if col.Unreadable > 0 {
			return errUnreadableValues
		}
	}
	return nil
}

// loadKeyring resolves the keys exactly as the backend does at boot.
func loadKeyring(ctx context.Context) (*keyring, error) {
	instance, err := encrypt.FromEnv()
	if err != nil {
		return nil, errors.New("CREDENTIALS_ENCRYPTION_KEY or CREDENTIALS_ENCRYPTION_READ_KEYS is invalid: each must be 64 hex characters")
	}
	if instance == nil {
		return nil, errors.New("CREDENTIALS_ENCRYPTION_KEY is not set. Run this inside the backend container, where the keys are configured.")
	}
	reads, _ := secretset.Parse(os.Getenv("CREDENTIALS_ENCRYPTION_READ_KEYS"))
	k := &keyring{instance: instance}
	for _, r := range reads {
		if r != os.Getenv("CREDENTIALS_ENCRYPTION_KEY") {
			k.instanceRead++
		}
	}

	var primary string
	var mfaReads []string
	if cfg, err := config.NewConfig(ctx); err == nil {
		if auth, aerr := cfg.LoadAuthConfig(ctx); aerr == nil {
			primary, mfaReads = auth.TwoFASecret, auth.TwoFAReadSecrets
		}
	}
	if primary == "" {
		primary = strings.TrimSpace(os.Getenv("TWOFA_SECRET"))
		if primary == "" {
			primary = strings.TrimSpace(os.Getenv("AUTH_SECRET"))
		}
		if mfaReads, err = secretset.Parse(os.Getenv("TWOFA_READ_SECRETS")); err != nil {
			return nil, fmt.Errorf("TWOFA_READ_SECRETS: %w", err)
		}
	}
	if primary != "" {
		key := twofa.DeriveKey(primary)
		k.mfa = &key
		for _, s := range mfaReads {
			if s != primary {
				k.mfaRead = append(k.mfaRead, twofa.DeriveKey(s))
			}
		}
	}
	return k, nil
}

// scanColumn classifies every value in one column and, with write, reseals the read-key ones by compare-and-swap.
func scanColumn(ctx context.Context, c *conn, keys *keyring, col sealedColumn, size int, write bool) (columnCount, error) {
	out := columnCount{sealedColumn: col}
	var exists bool
	if err := c.db.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, col.Table).Scan(&exists); err != nil {
		return out, fmt.Errorf("reading %s: %w", col.Table, err)
	}
	if !exists {
		out.Missing = true
		return out, nil
	}
	table, column := pgx.Identifier{col.Table}.Sanitize(), pgx.Identifier{col.Column}.Sanitize()
	filter := ""
	if col.guard != "" {
		filter = " AND " + pgx.Identifier{col.guard}.Sanitize()
	}
	query := fmt.Sprintf(`SELECT ctid::text AS row_tid, %[2]s::text FROM %[1]s WHERE %[2]s IS NOT NULL AND %[2]s::text <> ''%[3]s AND ctid > $1::tid ORDER BY ctid LIMIT $2`, table, column, filter)
	// The ctid and the old value together are the compare: a row rewritten meanwhile matches neither.
	update := fmt.Sprintf(`UPDATE %[1]s SET %[2]s = $1 WHERE ctid = $2::tid AND %[2]s::text = $3`, table, column)

	// One snapshot for the reads, so a tuple this run rewrites is never read back.
	snap, err := c.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("reading %s.%s: %w", col.Table, col.Column, err)
	}
	defer func() { _ = snap.Rollback(ctx) }()

	after := "(0,0)"
	for {
		rows, err := snap.Query(ctx, query, after, size)
		if err != nil {
			return out, fmt.Errorf("reading %s.%s: %w", col.Table, col.Column, err)
		}
		type row struct{ ctid, value string }
		var page []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.ctid, &r.value); err != nil {
				rows.Close()
				return out, fmt.Errorf("reading %s.%s: %w", col.Table, col.Column, err)
			}
			page = append(page, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return out, fmt.Errorf("reading %s.%s: %w", col.Table, col.Column, err)
		}
		for _, r := range page {
			resealed, changed, err := resealValue(keys, col, r.value)
			switch {
			case errors.Is(err, errUnsealed):
				out.Unsealed++
				continue
			case err != nil:
				out.Unreadable++
				continue
			case !changed:
				out.Primary++
				continue
			}
			out.ReadKey++
			if !write {
				continue
			}
			tag, err := c.db.Pool.Exec(ctx, update, resealed, r.ctid, r.value)
			if err != nil {
				return out, fmt.Errorf("resealing %s.%s: %w", col.Table, col.Column, err)
			}
			if tag.RowsAffected() == 1 {
				out.Resealed++
			} else {
				out.Raced++
			}
		}
		if len(page) < size {
			return out, nil
		}
		after = page[len(page)-1].ctid
	}
}

var errUnsealed = errors.New("value is stored outside the sealed format")

func resealValue(keys *keyring, col sealedColumn, value string) (string, bool, error) {
	if col.Domain == domainMFA {
		return twofa.Reseal(*keys.mfa, keys.mfaRead, value)
	}
	if !encrypt.LooksSealed(value) {
		return "", false, errUnsealed
	}
	return keys.instance.Reseal(value)
}

// graceWindows reports every previous-credential window the instance is configured with.
func graceWindows(now time.Time) []graceWindow {
	var out []graceWindow
	for _, w := range []struct{ name, list string }{
		{"INTERNAL_API_PREVIOUS_UNTIL", "INTERNAL_API_PREVIOUS_TOKENS"},
		{"NODE_BROKER_PREVIOUS_UNTIL", "NODE_BROKER_PREVIOUS_TOKENS"},
		{"UNSUBSCRIBE_SIGNING_READ_UNTIL", "UNSUBSCRIBE_SIGNING_READ_SECRETS"},
	} {
		raw := strings.TrimSpace(os.Getenv(w.name))
		entries, _ := secretset.Parse(os.Getenv(w.list))
		if raw == "" && len(entries) == 0 {
			continue
		}
		until, _ := time.Parse(time.RFC3339, raw)
		out = append(out, graceWindow{Name: w.name, Until: until, Open: now.Before(until), Entries: len(entries)})
	}
	return out
}

func printKeyReport(r keyReport, reseal bool) {
	if reseal && !r.Applied {
		fmt.Print("Dry run: nothing will be written. Pass --apply to reseal.\n\n")
	}
	fmt.Printf("Instance credential key: primary plus %s.\n", counted(r.InstanceReadKeys, "read key", "read keys"))
	if r.MFAConfigured {
		fmt.Printf("MFA key: primary plus %s.\n", counted(r.MFAReadKeys, "read key", "read keys"))
	} else {
		fmt.Println("MFA key: not configured here, so TOTP enrollments are not checked.")
	}
	fmt.Printf("\n  %-44s %8s %9s %9s %11s\n", "column", "primary", "read key", "unsealed", "unreadable")
	var onRead, unreadable, resealed, raced int
	cols := append([]columnCount(nil), r.Columns...)
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Domain < cols[j].Domain })
	for _, c := range cols {
		label := c.Table + "." + c.Column
		if c.Missing {
			fmt.Printf("  %-44s %s\n", label, "table not present")
			continue
		}
		fmt.Printf("  %-44s %8d %9d %9d %11d\n", label, c.Primary, c.ReadKey, c.Unsealed, c.Unreadable)
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
