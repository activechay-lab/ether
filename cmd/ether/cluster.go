package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/braedonsaunders/ether/internal/dburl"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/logredact"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

const clusterUsage = `usage:
  ether cluster retire-node --node-id ID --boot BOOT --evidence REFERENCE [--data-dir DIR]
      Attest that this exact boot is permanently removed/fenced; release its
      displaced encoder claims and prohibit that boot from new placements.
  ether cluster ledger-quota [--data-dir DIR] --expect N --capacity M
      Explicitly raise the durable fleet ledger ceiling with compare-and-swap.
      Then configure ledgerCapacity=M consistently on all replicas.
  ether cluster drain [--data-dir DIR] [--wait 120s]
      Stop deployment admissions and wait for exact local runtime retirement.
      Timeout returns nonzero and retains unresolved ownership.
  ether cluster init  [--data-dir DIR] [--database-url URL]
      Make this (stopped) standalone server the first node of a cluster:
      copy its SQLite data into an empty PostgreSQL database and share its
      settings from there. URL defaults to $ETHER_DATABASE_URL.
  ether cluster token [--data-dir DIR] [--database-url URL] [--ttl 1h]
      Print a one-time join token for a new node (valid for --ttl, at most
      1h). URL is the database address as the new node will reach it
      (default: this node's).
  ether cluster join  [--data-dir DIR] [--database-url URL] [--force] TOKEN
      Make this data directory a node of the cluster the token belongs to.
      --force moves an already configured server's settings aside.
`

// runCluster handles `ether cluster ...` and returns the exit code.
func runCluster(args []string, defaultDataDir string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stderr, clusterUsage)
		return 2
	}
	cmd := args[0]
	fs := flag.NewFlagSet("ether cluster "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", defaultDataDir, "data directory")
	dbURL := fs.String("database-url", "", "PostgreSQL URL (postgres://...)")
	force := fs.Bool("force", false, "join: move an existing configured server aside")
	expectedQuota := fs.Int("expect", 0, "ledger-quota: expected current fleet ceiling")
	newQuota := fs.Int("capacity", 0, "ledger-quota: new fleet ceiling")
	nodeID := fs.String("node-id", "", "retire-node: exact removed node ID")
	boot := fs.String("boot", "", "retire-node: exact removed boot incarnation")
	evidence := fs.String("evidence", "", "retire-node: operator removal/fencing evidence reference")
	wait := fs.Duration("wait", 2*time.Minute, "drain: maximum wait for quiescence")
	fs.DurationVar(wait, "timeout", 2*time.Minute, "drain: alias for --wait")
	ttl := fs.Duration("ttl", clusterconfig.DefaultJoinTokenTTL, "token: validity")
	positional := []string{}
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log := slog.New(logredact.Handler(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	var err error
	switch cmd {
	case "retire-node":
		err = retireClusterNode(ctx, *dataDir, placement.Target{NodeID: *nodeID, Boot: *boot}, *evidence, log)
		if err == nil {
			fmt.Fprintln(stdout, "Exact node boot removed; displaced encoder claims released.")
		}
	case "ledger-quota":
		err = raiseLedgerQuota(ctx, *dataDir, *expectedQuota, *newQuota, log)
	case "drain":
		if *wait <= 0 || *wait > time.Hour {
			err = errors.New("drain wait must be positive and at most 1h")
			break
		}
		dctx, cancel := context.WithTimeout(ctx, *wait)
		defer cancel()
		err = clusterconfig.RequestDrain(dctx, *dataDir)
		if err == nil {
			fmt.Fprintln(stdout, "Node drained; no unresolved local runtime ownership.")
		}
	case "init":
		url := *dbURL
		if url == "" {
			url = strings.TrimSpace(os.Getenv(config.DatabaseURLEnv))
		}
		fmt.Fprintln(stdout, "Make sure Ether is stopped on this machine; changes made while copying would be lost.")
		err = clusterconfig.Init(ctx, clusterconfig.InitOptions{DataDir: *dataDir, DatabaseURL: url, Logger: log})
		if err == nil {
			fmt.Fprintf(stdout, "This server is now the first node of a cluster on %s.\nStart Ether, then add nodes with `ether cluster token` here and `ether cluster join` there.\n", dburl.Redact(url))
		}
	case "token":
		err = clusterToken(ctx, stdout, *dataDir, *dbURL, *ttl, log)
	case "join":
		if len(positional) != 1 {
			fmt.Fprint(stderr, clusterUsage)
			return 2
		}
		var res clusterconfig.JoinResult
		res, err = clusterconfig.Join(ctx, clusterconfig.JoinOptions{DataDir: *dataDir, Token: positional[0], DatabaseURL: *dbURL, Force: *force, Logger: log})
		if err == nil {
			for _, p := range res.MovedAside {
				fmt.Fprintf(stdout, "Moved aside: %s\n", p)
			}
			fmt.Fprintf(stdout, "Joined the cluster as server %s. Start Ether on this machine (forward its own port for remote access).\n", res.MachineIdentifier)
		}
	default:
		fmt.Fprint(stderr, clusterUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "ether cluster "+cmd+":", err)
		return 1
	}
	return 0
}

func clusterToken(ctx context.Context, stdout io.Writer, dataDir, url string, ttl time.Duration, log *slog.Logger) error {
	if ttl <= 0 || ttl > time.Hour {
		return errors.New("--ttl must be between 1s and 1h")
	}
	cfg, err := config.Open(dataDir)
	if err != nil {
		return err
	}
	dbCfg := cfg.Get().Database.Effective(os.Getenv)
	if dbCfg.Driver != config.DatabasePostgres {
		return errors.New("clustering is not enabled on this server (it uses SQLite); run `ether cluster init` first")
	}
	db, _, err := store.OpenWith(ctx, store.Options{Driver: dbCfg.Driver, URL: dbCfg.URL, Logger: log})
	if err != nil {
		return err
	}
	defer db.Close()
	if url == "" {
		url = dbCfg.URL
	}
	tok, info, err := clusterconfig.NewToken(ctx, db, cfg, url, ttl)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s\n\nOn the new node run:\n  ether cluster join <token>\n\nValid until %s, once.\n%s\n", tok.String(), info.ExpiresAt.Local().Format(time.RFC1123), clusterconfig.TokenWarning)
	return nil
}

func raiseLedgerQuota(ctx context.Context, dataDir string, expected, capacity int, log *slog.Logger) error {
	if expected < 1 || capacity <= expected || capacity < 4096 || capacity > 10000000 {
		return errors.New("ledger-quota requires --expect N and a larger --capacity M in 4096..10000000")
	}
	cfg, err := config.Open(dataDir)
	if err != nil {
		return errors.New("ledger-quota: cannot read local cluster credentials")
	}
	defer cfg.Close()
	dbCfg := cfg.Get().Database
	if !dbCfg.IsPostgres() {
		return errors.New("ledger-quota requires an enrolled PostgreSQL node")
	}
	db, _, err := store.OpenWith(ctx, store.Options{Driver: dbCfg.Driver, URL: dbCfg.URL, Logger: log})
	if err != nil {
		return errors.New("ledger-quota: cannot open cluster database")
	}
	defer db.Close()
	doc, found, err := clusterconfig.NewShared(db).LoadShared(ctx)
	if err != nil || !found || doc.KeyFingerprint != cfg.KeyFingerprint() {
		return errors.New("ledger-quota: cluster identity could not be verified")
	}
	service, err := placement.New(db, placement.Options{MaxSessions: capacity, MaxLease: 10 * time.Minute, LockTimeout: 5 * time.Second}, nil)
	if err != nil {
		return err
	}
	return service.RaiseLedgerQuota(ctx, expected)
}

func retireClusterNode(ctx context.Context, dataDir string, target placement.Target, evidence string, log *slog.Logger) error {
	if target.NodeID == "" || target.Boot == "" || evidence == "" {
		return errors.New("retire-node requires --node-id, --boot and --evidence")
	}
	cfg, err := config.Open(dataDir)
	if err != nil {
		return errors.New("retire-node: cannot read local cluster credentials")
	}
	defer cfg.Close()
	dbCfg := cfg.Get().Database
	if !dbCfg.IsPostgres() {
		return errors.New("retire-node requires an enrolled PostgreSQL node")
	}
	db, _, err := store.OpenWith(ctx, store.Options{Driver: dbCfg.Driver, URL: dbCfg.URL, Logger: log})
	if err != nil {
		return errors.New("retire-node: cannot open cluster database")
	}
	defer db.Close()
	doc, found, err := clusterconfig.NewShared(db).LoadShared(ctx)
	if err != nil || !found || doc.KeyFingerprint != cfg.KeyFingerprint() {
		return errors.New("retire-node: cluster identity could not be verified")
	}
	service, err := placement.New(db, placement.Options{MaxSessions: 1, MaxLease: 10 * time.Minute, LockTimeout: 5 * time.Second}, nil)
	if err != nil {
		return err
	}
	return service.RemoveTarget(ctx, target, evidence)
}
