package main

// etherperf baseline measures any Plex-protocol server in a Docker
// container (a stock PMS or Ether) with the clients' own request sequences:
// the per-step cold request after a restart with the server's files evicted
// from the page cache, warm latency percentiles, response sizes, and the
// server's CPU time and block IO per request from its cgroup. etherperf
// fixture builds an Ether data directory at a synthetic scale to run that
// way, and etherperf compare puts several baselines side by side. See
// docs/performance.md.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/perfharness"
)

// viewerFile is the measured viewer etherperf fixture writes beside a data
// directory. It holds a minted token for a throwaway copy, never a real one.
type viewerFile struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

func runBaseline(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("baseline", flag.ExitOnError)
	target := f.String("url", "", "the server's base URL, e.g. http://172.22.0.2:32400")
	container := f.String("container", "", "the server's Docker container (restarted for each cold step; its cgroup is the probe)")
	label := f.String("label", "", "label for the report")
	viewerPath := f.String("viewer", "", "viewer JSON from etherperf fixture (default: no token, for a server that trusts this network)")
	only := f.String("clients", "plex-appletv,plex-web", "comma-separated client names")
	stepNames := f.String("steps", "", "comma-separated substrings of step names (default all)")
	rounds := f.Int("rounds", 20, "warm rounds")
	coldClients := f.String("cold-clients", "", "clients whose steps each get a cold restart (default all of -clients; \"none\" to skip)")
	drop := f.String("drop", "", "comma-separated paths inside the container whose files are evicted from the page cache before each cold start")
	settleMax := f.Duration("settle-max", 3*time.Minute, "longest wait for the server to go idle after a start")
	out := f.String("out", "", "output directory")
	_ = f.Parse(args)
	if *target == "" || *container == "" || *out == "" {
		return errors.New("-url, -container and -out are required")
	}
	u, err := url.Parse(*target)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("bad -url %q", *target)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return fmt.Errorf("-url needs a port: %q", *target)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	log := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "etherperf: "+format+"\n", a...) }
	viewer := perfharness.Viewer{ID: 1, Username: "admin", Owner: true}
	if *viewerPath != "" {
		var vf viewerFile
		b, err := os.ReadFile(*viewerPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &vf); err != nil {
			return err
		}
		viewer = perfharness.Viewer{ID: vf.ID, Username: vf.Username, Owner: true, PlexToken: vf.Token}
	}
	cs := perfharness.Only(perfharness.Clients(), strings.Split(*only, ","))
	if *stepNames != "" {
		cs = perfharness.OnlySteps(cs, strings.Split(*stepNames, ","))
	}
	if len(cs) == 0 {
		return errors.New("no clients selected")
	}
	cold := cs
	switch *coldClients {
	case "":
	case "none":
		cold = nil
	default:
		cold = perfharness.Only(cs, strings.Split(*coldClients, ","))
	}
	d := &dockerServer{name: *container, base: strings.TrimRight(*target, "/"), settleMax: *settleMax, log: log}
	if *drop != "" {
		d.drop = strings.Split(*drop, ",")
	}
	if err := d.inspect(ctx); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	d.self = self

	meta := []string{fmt.Sprintf("container %s, image %s", *container, d.image), "host load before: " + loadavg()}
	log("restart, then discover %s's ids", *label)
	if err := d.restart(ctx); err != nil {
		return err
	}
	identity := d.identity(ctx)
	meta = append(meta, "identity: "+identity, fmt.Sprintf("first start: ready %s, idle %s", d.lastReady.Round(time.Millisecond), d.lastSettle.Round(time.Millisecond)))
	idleCPU := d.idleRate(ctx, 5*time.Second)
	meta = append(meta, fmt.Sprintf("idle CPU: %.1f ms/s over 5 s", float64(idleCPU)/float64(time.Millisecond)))

	r := perfharness.NewRunner(port, false, []perfharness.Viewer{viewer})
	r.SetHost(u.Hostname())
	r.SetTimeout(2 * time.Minute)
	r.Measure(perfharness.CgroupProbe{Dir: d.cgroup})
	r.Sequential(ctx, "discover", []perfharness.Viewer{viewer}, cs)

	var readies, settles []time.Duration
	if len(cold) > 0 {
		n := 0
		for _, c := range cold {
			n += len(c.Steps)
		}
		log("cold: %d steps, each after a restart", n)
		i := 0
		err := r.ColdEach(ctx, "cold", viewer, cold, func(ctx context.Context, c perfharness.Client, st perfharness.Step) error {
			i++
			if i%10 == 1 {
				log("cold %d/%d: %s %s", i, n, c.Name, st.Name)
			}
			if err := d.restart(ctx); err != nil {
				return err
			}
			readies, settles = append(readies, d.lastReady), append(settles, d.lastSettle)
			return nil
		})
		if err != nil {
			return err
		}
		meta = append(meta, fmt.Sprintf("cold starts: %d; ready after p50 %s (max %s), then idle after p50 %s (max %s)", len(readies),
			p50(readies), maxDur(readies), p50(settles), maxDur(settles)))
	}
	log("warm: one priming pass, then %d rounds", *rounds)
	r.Sequential(ctx, "prime", []perfharness.Viewer{viewer}, cs)
	start := time.Now()
	for range *rounds {
		r.Sequential(ctx, "warm", []perfharness.Viewer{viewer}, cs)
	}
	meta = append(meta, fmt.Sprintf("warm: %d sequential rounds in %s", *rounds, time.Since(start).Round(time.Millisecond)))
	cur, peak := perfharness.CgroupMemory(d.cgroup)
	meta = append(meta, fmt.Sprintf("memory after warm rounds: %d MB (peak since last start %d MB)", cur>>20, peak>>20), "host load after: "+loadavg())

	rows := perfharness.SummarizeBaseline(r.Results(), nil, cs)
	if err := writeJSON(filepath.Join(*out, "results.json"), r.Results()); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(*out, "baseline.json"), baselineFile{Label: *label, Meta: meta, Rows: rows}); err != nil {
		return err
	}
	var buf bytes.Buffer
	perfharness.WriteBaselineReport(&buf, "Baseline: "+*label, meta, rows)
	if err := os.WriteFile(filepath.Join(*out, "report.md"), buf.Bytes(), 0o600); err != nil {
		return err
	}
	log("report: %s", filepath.Join(*out, "report.md"))
	return nil
}

type baselineFile struct {
	Label string                    `json:"label"`
	Meta  []string                  `json:"meta"`
	Rows  []perfharness.BaselineRow `json:"rows"`
}

func runCompare(args []string) error {
	f := flag.NewFlagSet("compare", flag.ExitOnError)
	client := f.String("client", "plex-appletv", "client whose steps to compare (empty for all)")
	_ = f.Parse(args)
	var targets []perfharness.Target
	for _, p := range f.Args() {
		var bf baselineFile
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &bf); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		targets = append(targets, perfharness.Target{Label: bf.Label, Rows: bf.Rows})
	}
	if len(targets) == 0 {
		return errors.New("usage: etherperf compare [-client NAME] baseline.json...")
	}
	perfharness.WriteComparison(os.Stdout, targets, *client)
	return nil
}

// dockerServer restarts a containerized server between cold steps.
type dockerServer struct {
	name, base, image, id, cgroup, self string
	drop                                []string
	settleMax                           time.Duration
	lastReady, lastSettle               time.Duration
	log                                 func(string, ...any)
}

func (d *dockerServer) inspect(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Id}} {{.Config.Image}}", d.name).Output()
	if err != nil {
		return fmt.Errorf("docker inspect %s: %w", d.name, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return fmt.Errorf("docker inspect %s: %q", d.name, out)
	}
	d.id, d.image = fields[0], fields[1]
	return nil
}

// restart stops the container, evicts its files from the page cache, starts
// it, and waits until it answers and then goes idle.
func (d *dockerServer) restart(ctx context.Context) error {
	if err := docker(ctx, "stop", "-t", "60", d.name); err != nil {
		return err
	}
	if len(d.drop) > 0 {
		// A helper from the same image, with the same mounts, so the image's
		// own files (the server binary and libraries) are evicted too.
		args := []string{"run", "--rm", "--name", d.name + "-dropcache", "--network", "none", "--volumes-from", d.name,
			"-v", d.self + ":/etherperf-dropcache:ro", "--entrypoint", "/etherperf-dropcache", d.image, "dropcache"}
		if err := docker(ctx, append(args, d.drop...)...); err != nil {
			return fmt.Errorf("drop cache: %w", err)
		}
	}
	start := time.Now()
	if err := docker(ctx, "start", d.name); err != nil {
		return err
	}
	cg, err := perfharness.DockerCgroup(d.id)
	if err != nil {
		return err
	}
	d.cgroup = cg
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	d.lastReady = time.Since(start)
	d.lastSettle = d.settle(ctx)
	return nil
}

// waitReady waits for /identity to answer 200 without PMS's startState
// (which it reports while its plug-ins start).
func (d *dockerServer) waitReady(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		resp, err := client.Get(d.base + "/identity")
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && !bytes.Contains(b, []byte("startState")) {
			return nil
		}
	}
	return fmt.Errorf("%s did not become ready", d.name)
}

// settle waits until the server's whole cgroup uses under 5% of a core for
// two consecutive seconds, so a cold request does not meet start-up work
// (index builds, plug-in start) that a real first visitor would usually
// miss. It gives up after settleMax.
func (d *dockerServer) settle(ctx context.Context) time.Duration {
	start := time.Now()
	quiet := 0
	for time.Since(start) < d.settleMax && quiet < 2 {
		if d.idleRate(ctx, time.Second) < 50*time.Millisecond {
			quiet++
		} else {
			quiet = 0
		}
	}
	return time.Since(start)
}

// idleRate is the server's CPU time per second over window.
func (d *dockerServer) idleRate(ctx context.Context, window time.Duration) time.Duration {
	p := perfharness.CgroupProbe{Dir: d.cgroup}
	a, err := p.Sample()
	if err != nil {
		return 0
	}
	select {
	case <-ctx.Done():
	case <-time.After(window):
	}
	b, err := p.Sample()
	if err != nil {
		return 0
	}
	return time.Duration(float64(b.CPU-a.CPU) / window.Seconds())
}

func (d *dockerServer) identity(ctx context.Context) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.base+"/identity", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var parts []string
	for _, attr := range []string{"version", "claimed", "machineIdentifier"} {
		// The root element's attributes; not the XML declaration's version.
		if m := regexp.MustCompile(`\s` + attr + `="([^"]*)"`).FindAllSubmatch(b, -1); m != nil {
			m := m[len(m)-1]
			v := string(m[1])
			if attr == "machineIdentifier" && len(v) > 8 {
				v = v[:8] + "…"
			}
			parts = append(parts, attr+"="+v)
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(string(b[:min(len(b), 120)]))
	}
	return strings.Join(parts, " ")
}

func docker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %w: %s", args[0], err, bytes.TrimSpace(out))
	}
	return nil
}

// runFixture builds an Ether data directory holding a synthetic library at
// one of the gate's scales, configured to serve on 0.0.0.0 (for a private
// container network), and writes the owner's minted credentials beside it.
func runFixture(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("fixture", flag.ExitOnError)
	scaleName := f.String("scale", "small", "small (the gate's 54k rows) or large (540k)")
	out := f.String("out", "", "new directory: OUT/data is the data directory, OUT/viewer.json the owner")
	port := f.Int("port", 32400, "Plex port inside the container")
	bin := f.String("bin", "", "ether binary that migrates the new database (default: build ./cmd/ether)")
	_ = f.Parse(args)
	scale := map[string]perfharness.Scale{"small": perfharness.SmallScale, "large": perfharness.LargeScale}[*scaleName]
	if scale.Movies == 0 || *out == "" {
		return errors.New("-scale small|large and -out are required")
	}
	dir := filepath.Join(absPath(*out), "data")
	if fileExists(dir) {
		return fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return err
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Frontends = map[string]config.Frontend{"plex": {Enabled: true}, "jellyfin": {Enabled: true}, "emby": {Enabled: true}, "silo": {Enabled: true}, "stremio": {Enabled: true}}
		c.FrontendPorts = map[string]int{"jellyfin": *port + 1, "emby": *port + 2, "silo": *port + 3}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Server.Port = *port
		c.Server.BindAddress = "0.0.0.0"
		return nil
	}); err != nil {
		return err
	}
	if err := sanitizeConfig(filepath.Join(dir, "config.json"), *port, false); err != nil {
		return err
	}
	// The server applies every module's migrations (this tool registers
	// only the store's own), so it creates the schema: start it once on the
	// empty directory, on a loopback port, and stop it.
	server := *bin
	if server == "" {
		server = filepath.Join(absPath(*out), "ether")
		if err := buildServer(ctx, server); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}
	if err := setBind(filepath.Join(dir, "config.json"), "127.0.0.1"); err != nil {
		return err
	}
	if _, err := migrateCopy(ctx, server, dir, *port+100, absPath(*out)); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := setBind(filepath.Join(dir, "config.json"), "0.0.0.0"); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ether.db")+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return err
	}
	defer db.Close()
	start := time.Now()
	if err := perfharness.Generate(ctx, db, scale, time.Now()); err != nil {
		return err
	}
	viewers, err := perfharness.MintCredentials(ctx, db, 1)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "etherperf: %d catalogue rows in %s\n", scale.Rows(), time.Since(start).Round(time.Millisecond))
	return writeJSON(filepath.Join(absPath(*out), "viewer.json"), viewerFile{ID: viewers[0].ID, Username: viewers[0].Username, Token: viewers[0].PlexToken})
}

func setBind(path, addr string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	server, _ := c["server"].(map[string]any)
	if server == nil {
		return errors.New("config has no server section")
	}
	server["bindAddress"] = addr
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func loadavg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "unknown"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "unknown"
	}
	return strings.Join(f[:3], " ") + fmt.Sprintf(" (1/5/15 min, %d CPUs)", runtime.NumCPU())
}

func p50(ds []time.Duration) string {
	if len(ds) == 0 {
		return "–"
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2].Round(time.Millisecond).String()
}

func maxDur(ds []time.Duration) string {
	var m time.Duration
	for _, d := range ds {
		m = max(m, d)
	}
	return m.Round(time.Millisecond).String()
}
