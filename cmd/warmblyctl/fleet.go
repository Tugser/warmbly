package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/fleetnode"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func fleetUsage(w *os.File) {
	fmt.Fprint(w, `Manage the machines running Warmbly.

  warmblyctl fleet join-token          Issue a join token (--ttl, default 168h). Shown once.
  warmblyctl fleet list                Every node: role, version, liveness, usage.
  warmblyctl fleet show <node-id>      One node in full.
  warmblyctl fleet remove <node-id>    Forget a node. Its mailboxes re-place themselves.
  warmblyctl fleet pin <node-id> <ver> Hold one node at a version ("" clears the pin).
  warmblyctl fleet version             What version the fleet should be running.
  warmblyctl fleet version <tag>       Pin the whole fleet to a tag.
  warmblyctl fleet channel <name>      Follow stable, dev, or pinned.
  warmblyctl fleet rollout start <tag> Move the fleet in stages: one canary, then waves.
                                       --digests key=sha256:...,... --canary 1 --waves 100
                                       --bake 30m --deadline 15m
  warmblyctl fleet rollout status      Stage, admitted nodes, and why it halted.
  warmblyctl fleet rollout advance     Admit the next stage now, skipping the bake.
  warmblyctl fleet rollout halt        Stop admitting; admitted nodes return to From.
  warmblyctl fleet rollout abort       Discard the rollout, leaving the fleet on From.
  warmblyctl fleet env-status          Whether every node has applied the current env.

Adding a machine is two commands: issue a token here, then on that machine run

  curl -fsSL https://<your-instance>/join.sh | sh -s -- \
    --url https://<your-instance> --token <token> --role worker

Roles are worker (sends and syncs mail) and consumer (processes events).
`)
}

func runFleet(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fleetUsage(os.Stderr)
		return errors.New("`fleet` needs a subcommand. Pick one from the list above.")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fleetUsage(os.Stdout)
		return nil
	case "join-token":
		return runFleetJoinToken(ctx, args[1:])
	case "list":
		return runFleetList(ctx, args[1:])
	case "show":
		return runFleetShow(ctx, args[1:])
	case "remove":
		return runFleetRemove(ctx, args[1:])
	case "pin":
		return runFleetPin(ctx, args[1:])
	case "version":
		return runFleetVersion(ctx, args[1:])
	case "channel":
		return runFleetChannel(ctx, args[1:])
	case "rollout":
		return runFleetRollout(ctx, args[1:])
	case "env-status":
		return runFleetEnvStatus(ctx, args[1:])
	}
	fleetUsage(os.Stderr)
	return fmt.Errorf("unknown fleet subcommand %q", args[0])
}

// fleetDeps is the small slice of the object graph the fleet commands need.
func fleetDeps(ctx context.Context) (*conn, repository.FleetNodeRepository, repository.FleetSettingsRepository, *fleetnode.Service, error) {
	c, err := connect(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	nodes := repository.NewFleetNodeRepository(c.db)
	settings := repository.NewFleetSettingsRepository(c.db)
	workers := repository.NewWorkerRepository(c.db.Pool)
	return c, nodes, settings, fleetnode.New(nodes, workers, settings), nil
}

func runFleetJoinToken(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet join-token")
	ttl := fs.Duration("ttl", fleetnode.DefaultJoinTokenTTL, "how long the token can join machines, at most 720h")
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

	if *ttl <= 0 {
		return fmt.Errorf("--ttl must be greater than zero")
	}
	token, expiresAt, err := svc.IssueJoinToken(ctx, *ttl)
	if err != nil {
		return err
	}
	fmt.Println(token)
	fmt.Fprintf(os.Stderr, "\nShown once. It joins any number of machines until %s. Issuing another token revokes this one; nodes already joined are unaffected.\n", expiresAt.Format(time.RFC3339))
	return nil
}

func runFleetList(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet list")
	role := fs.String("role", "", "only worker or consumer")
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

	nodes, err := svc.List(ctx, models.NodeRole(*role))
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(nodes)
	}
	if len(nodes) == 0 {
		fmt.Println("No nodes have joined yet. Issue a token with `warmblyctl fleet join-token`.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tNAME\tSTATE\tVERSION\tREGION\tMEM\tSEEN\tID")
	for _, n := range nodes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			n.Role, orDash(n.Name), nodeState(&n), versionCell(&n),
			orDash(n.Region), memCell(&n), seenCell(&n), n.ID)
	}
	return w.Flush()
}

// nodeState is the one word that matters: can this node do work right now.
func nodeState(n *models.FleetNode) string {
	switch {
	case !n.Active:
		return "stopped"
	case n.Live():
		return "live"
	default:
		return "unreachable"
	}
}

// versionCell shows the pending update inline, because "which of my machines
// are behind" is the question this table exists to answer.
func versionCell(n *models.FleetNode) string {
	cur := orDash(n.Version)
	if n.NeedsUpdate() {
		return cur + " -> " + n.DesiredVersion
	}
	return cur
}

func memCell(n *models.FleetNode) string {
	if n.Usage.MemoryMB == nil {
		return "-"
	}
	return fmt.Sprintf("%dMB", *n.Usage.MemoryMB)
}

func seenCell(n *models.FleetNode) string {
	if n.LastSeenAt == nil {
		return "never"
	}
	d := time.Since(*n.LastSeenAt).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func runFleetShow(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("`fleet show` needs one node id")
	}
	id, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%q is not a node id", fs.Arg(0))
	}
	c, _, _, svc, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	node, err := svc.Get(ctx, id)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("no node with id %s", id)
	}
	return json.NewEncoder(os.Stdout).Encode(node)
}

func runFleetRemove(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet remove")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("`fleet remove` needs one node id")
	}
	id, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%q is not a node id", fs.Arg(0))
	}
	c, nodes, _, _, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	if err := nodes.Delete(ctx, id); err != nil {
		return err
	}
	// Mailboxes are released by the foreign key, not stranded: the rotation
	// loop places them on a live worker on its next pass.
	fmt.Printf("Removed %s. Any mailboxes it carried will be re-placed within a few minutes.\n", id)
	fmt.Println("Stop the service on that machine too, or it will re-join on its next heartbeat.")
	return nil
}

func runFleetPin(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet pin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return errors.New("usage: warmblyctl fleet pin <node-id> [version]   (omit the version to clear)")
	}
	id, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%q is not a node id", fs.Arg(0))
	}
	version := ""
	if fs.NArg() == 2 {
		version = fs.Arg(1)
	}
	c, nodes, _, _, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	if err := nodes.SetPinnedVersion(ctx, id, version); err != nil {
		return err
	}
	if version == "" {
		fmt.Printf("Cleared the pin on %s; it will follow the fleet version again.\n", id)
		return nil
	}
	fmt.Printf("Pinned %s to %s. It will hold there until the pin is cleared.\n", id, version)
	return nil
}

func runFleetVersion(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, _, settings, _, err := fleetDeps(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	if fs.NArg() == 0 {
		state, err := settings.GetRelease(ctx)
		if err != nil {
			return err
		}
		if state == nil || state.Tag == "" {
			fmt.Println("No fleet version resolved yet. Nodes are leaving themselves alone.")
			return nil
		}
		fmt.Printf("%s  (channel %s, resolved %s)\n", state.Tag, state.Channel,
			state.ResolvedAt.Format(time.RFC3339))
		return nil
	}
	if fs.NArg() != 1 {
		return errors.New("usage: warmblyctl fleet version [tag]")
	}

	tag := strings.TrimSpace(fs.Arg(0))
	next := &models.FleetReleaseState{
		Channel:    models.FleetChannelPinned,
		Tag:        tag,
		ResolvedAt: time.Now(),
		Source:     "warmblyctl",
	}
	if err := settings.SetRelease(ctx, next); err != nil {
		return err
	}
	fmt.Printf("Fleet target is now %s. Every node moves to it within a couple of minutes.\n", tag)
	fmt.Println("The channel is now `pinned`, so a new release will not override this. `fleet channel stable` resumes following releases.")
	return nil
}

func runFleetChannel(ctx context.Context, args []string) error {
	fs := newFlagSet("fleet channel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: warmblyctl fleet channel <stable|dev|pinned>")
	}
	channel := fs.Arg(0)
	switch channel {
	case models.FleetChannelStable, models.FleetChannelDev, models.FleetChannelPinned:
	default:
		return fmt.Errorf("channel must be stable, dev or pinned, got %q", channel)
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
	if state == nil {
		state = &models.FleetReleaseState{}
	}
	state.Channel = channel
	if err := settings.SetRelease(ctx, state); err != nil {
		return err
	}
	fmt.Printf("Fleet now follows the %s channel.\n", channel)
	if channel != models.FleetChannelPinned {
		fmt.Println("The backend resolves the head of that channel on its next release check.")
	}
	return nil
}
