package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/app/fleetnode"
	"github.com/warmbly/warmbly/internal/models"
)

func runFleetRollout(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: warmblyctl fleet rollout <start|status|advance|halt|abort>")
	}
	switch args[0] {
	case "start":
		return runFleetRolloutStart(ctx, args[1:])
	case "status":
		return runFleetRolloutStatus(ctx, args[1:])
	case "advance", "halt", "abort":
		return runFleetRolloutAction(ctx, args[0], args[1:])
	}
	return fmt.Errorf("unknown rollout subcommand %q", args[0])
}

func runFleetRolloutStart(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet rollout start")
	digests := fs.String("digests", "", "comma-separated image=sha256:... pairs (worker, worker-kafka, consumer, consumer-kafka)")
	canary := fs.Int("canary", 1, "nodes admitted first, alone")
	waves := fs.String("waves", "100", "comma-separated cumulative percentages admitted after the canary")
	bake := fs.Duration("bake", 30*time.Minute, "healthy time on the target before the next stage")
	deadline := fs.Duration("deadline", 15*time.Minute, "time an admitted node has to report the target")
	asJSON := fs.Bool("json", false, "machine-readable output")
	tag, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}
	policy := models.FleetRolloutPolicy{Canary: *canary, BakeSeconds: int(bake.Seconds()), DeadlineSeconds: int(deadline.Seconds())}
	for _, w := range strings.Split(*waves, ",") {
		if w = strings.TrimSpace(w); w == "" {
			continue
		}
		pct, err := strconv.Atoi(w)
		if err != nil || pct <= 0 || pct > 100 {
			return fmt.Errorf("--waves takes percentages between 1 and 100, got %q", w)
		}
		policy.Waves = append(policy.Waves, pct)
	}
	pins := map[string]string{}
	for _, pair := range strings.Split(*digests, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("--digests entries are image=sha256:..., got %q", pair)
		}
		pins[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	c, _, _, svc, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	state, err := svc.StartRollout(ctx, tag, pins, policy)
	if err != nil {
		return err
	}
	return printRollout(state, *asJSON)
}

// parseWithPositional accepts the one positional argument before or after flags.
func parseWithPositional(fs interface {
	Parse([]string) error
	NArg() int
	Arg(int) string
}, args []string) (string, error) {
	var positional string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if positional == "" && fs.NArg() == 1 {
		positional = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return "", errors.New("expected exactly one tag")
	}
	if positional == "" {
		return "", errors.New("a tag is required")
	}
	return positional, nil
}

func runFleetRolloutStatus(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet rollout status")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := noExtraArgs(fs); err != nil {
		return err
	}
	c, _, settings, _, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	state, err := settings.GetRelease(ctx)
	if err != nil {
		return err
	}
	return printRollout(state, *asJSON)
}

func runFleetRolloutAction(ctx context.Context, action string, args []string) error {
	fs := newFlagSet("fleet rollout " + action)
	reason := fs.String("reason", "", "why, recorded with a halt")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := noExtraArgs(fs); err != nil {
		return err
	}
	c, _, _, svc, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	var state *models.FleetReleaseState
	switch action {
	case "advance":
		state, err = svc.AdvanceRollout(ctx, true)
	case "halt":
		state, err = svc.HaltRollout(ctx, *reason)
	default:
		state, err = svc.AbortRollout(ctx)
	}
	if err != nil {
		return err
	}
	return printRollout(state, *asJSON)
}

func printRollout(state *models.FleetReleaseState, asJSON bool) error {
	if state == nil {
		state = &models.FleetReleaseState{}
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(state)
	}
	r := state.Rollout
	if r == nil {
		fmt.Printf("No rollout in progress. Fleet target: %s\n", orDash(state.Tag))
		return nil
	}
	fmt.Printf("Rollout %s -> %s: %s", orDash(r.From), r.Target, r.Stage)
	if r.Stage == models.FleetRolloutWave {
		fmt.Printf(" %d of %d", r.Wave+1, len(r.Policy.Waves))
	}
	fmt.Printf(", %d admitted, stage started %s\n", len(r.Admitted), r.StageStartedAt.Format(time.RFC3339))
	if r.HaltReason != "" {
		fmt.Println("Halted:", r.HaltReason)
		fmt.Println("Admitted nodes return to", orDash(r.From)+". Fix the cause, then `fleet rollout start` again or `fleet rollout abort`.")
	}
	return nil
}

// runFleetEnvStatus compares each live node's applied env revision with the
// env this process would render, which matches the backend's when it runs
// from the backend's own task configuration.
func runFleetEnvStatus(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet env-status")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := noExtraArgs(fs); err != nil {
		return err
	}
	c, nodes, _, _, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	list, err := nodes.List(ctx, "")
	if err != nil {
		return err
	}
	report := fleetnode.EnvStatusOf(list)
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		for _, n := range report.Nodes {
			state := "stale"
			switch {
			case n.Current:
				state = "current"
			case !n.Tracked:
				state = "manual"
			}
			fmt.Printf("%-8s %-24s %-8s %s\n", n.Role, n.Name, state, n.ID)
		}
	}
	if !report.AllCurrent {
		return errors.New("some live nodes have not applied the current env")
	}
	return nil
}
