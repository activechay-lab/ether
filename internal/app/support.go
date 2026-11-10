package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/diagnostics"
	"github.com/braedonsaunders/ether/internal/logfile"
	"github.com/braedonsaunders/ether/internal/updater"
)

func (a *App) supportDeps() diagnostics.Deps {
	started := time.Now()
	container := containerHint()
	return diagnostics.Deps{
		Now:          parityClock(),
		Version:      a.opts.Version,
		Commit:       updater.Commit,
		BuildDate:    updater.Date,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		Container:    container,
		InstallType:  supportInstallType(container),
		Uptime:       func() time.Duration { return time.Since(started) },
		Secrets:      func() []string { return supportSecrets(a.cfg.Get()) },
		Settings:     func() (diagnostics.Settings, error) { return supportSettings(a.cfg.Get()), nil },
		Sessions:     a.supportSessions,
		Hardware:     a.supportHardware,
		Storage:      a.supportStorage,
		Connectivity: func() (diagnostics.ConnectivityFact, error) { return supportConnectivity(a.cfg.Get()), nil },
		Viewer:       a.supportViewer,
		LogExtract:   a.supportLogs,
	}
}

func supportSecrets(cfg config.Config) []string {
	out := config.ProtectedValues(cfg)
	if v := strings.TrimSpace(os.Getenv("ETHER_DATABASE_URL")); v != "" {
		out = append(out, v)
	}
	return out
}

func supportSettings(cfg config.Config) diagnostics.Settings {
	name, height, hdr := profileFacts(cfg, cfg.Quality.Default)
	out := diagnostics.Settings{
		LogLevel:              cfg.Server.LogLevel,
		FriendlyName:          cfg.Server.FriendlyName,
		QualityDefault:        cfg.Quality.Default,
		QualityRemote:         cfg.Quality.Remote,
		ProfileName:           name,
		ProfileMaxHeight:      height,
		ProfileAllowHDR:       hdr,
		HWAccel:               cfg.Transcode.HWAccel,
		MaxTranscodes:         cfg.Transcode.MaxTranscodes,
		RemoteMaxBitrateKbps:  cfg.Transcode.RemoteMaxBitrateKbps,
		SecureConnections:     cfg.Plex.SecureConnections,
		RemoteAccess:          cfg.Plex.RemoteAccess,
		CertificateConfigured: cfg.Plex.CertFile != "" || cfg.Plex.KeyFile != "",
		CacheBudgetBytes:      cfg.Cache.BudgetBytes,
		ReleaseMode:           cfg.Availability.EffectiveMode(),
		ShowUpcomingEpisodes:  cfg.Catalog.ShowUpcomingEpisodes,
		ShowUpcomingMovies:    cfg.Catalog.ShowUpcomingMovies,
		VisibilityDefault:     cfg.Catalog.VisibilityDefault,
	}
	for _, r := range cfg.Catalog.Exclusions {
		if len(out.CatalogRules) >= 100 {
			out.CatalogRulesTruncated = true
			break
		}
		rule := diagnostics.CatalogRuleFact{Action: r.Action, MediaType: r.MediaType, Match: r.Match, Scope: r.Scope}
		for _, c := range r.Rules {
			if len(rule.Conditions) >= 50 {
				out.CatalogRulesTruncated = true
				break
			}
			value := ""
			if len(c.Value) <= 4096 {
				value = string(c.Value)
			} else {
				out.CatalogRulesTruncated = true
			}
			rule.Conditions = append(rule.Conditions, diagnostics.CatalogConditionFact{Field: c.Field, Op: c.Op, Value: value})
		}
		out.CatalogRules = append(out.CatalogRules, rule)
	}
	return out
}

func profileFacts(cfg config.Config, name string) (string, int, bool) {
	for _, p := range cfg.Quality.Profiles {
		if p.Name == name {
			return p.Name, p.MaxHeight, p.AllowHDR
		}
	}
	return name, 0, false
}

func (a *App) supportSessions(deviceKey string) ([]diagnostics.SessionFact, int, error) {
	if a.playback == nil {
		return nil, 0, errors.New("playback unavailable")
	}
	var matched []diagnostics.SessionFact
	omitted := 0
	for _, s := range a.playback.Sessions() {
		if s == nil {
			continue
		}
		if deviceKey == "" || s.Client.ClientID != deviceKey {
			omitted++
			continue
		}
		state := "open"
		if s.ViewOffset != nil {
			state = "reported"
		}
		matched = append(matched, diagnostics.SessionFact{
			DeviceKey: s.Client.ClientID,
			State:     state,
			Decision:  string(s.Decision),
			Title:     s.TitleName,
			Username:  s.Username,
			Bytes:     s.BytesServed,
		})
	}
	return matched, omitted, nil
}

func (a *App) supportHardware() (diagnostics.HardwareFact, error) {
	if a.transcoder == nil {
		return diagnostics.HardwareFact{}, errors.New("transcoder unavailable")
	}
	hw := a.transcoder.HWAccel()
	return diagnostics.HardwareFact{Name: hw.Name, FFmpeg: a.transcoder.Version(), Available: hw.Name != "" && hw.Name != "none"}, nil
}

func (a *App) supportStorage() (diagnostics.StorageFact, error) {
	cfg := a.cfg.Get()
	fact := diagnostics.StorageFact{CacheBudgetBytes: cfg.Cache.BudgetBytes}
	collectDatabaseStorage(a.db, &fact)
	collectStorageDetail(a.db, a.opts.DataDir, cfg.Catalog, &fact)
	fact.Filesystem = supportFilesystem(a.opts.DataDir)
	if a.cache != nil {
		dir := a.cache.Directory()
		fact.CacheFilesystem = supportFilesystem(dir)
		if free, err := admin.DiskFree(dir); err == nil {
			fact.CacheFreeBytes = free
			fact.CacheFreeKnown = true
		}
	}
	if info, err := os.Stat(filepath.Join(a.opts.DataDir, "ether.db")); err == nil {
		fact.DatabaseBytes = info.Size()
		fact.DatabaseKnown = true
	}
	if info, err := os.Stat(filepath.Join(a.opts.DataDir, "ether.db-wal")); err == nil {
		fact.WALBytes = info.Size()
		fact.WALKnown = true
	} else if fact.DatabaseKnown && errors.Is(err, os.ErrNotExist) {
		fact.WALKnown = true
	}
	if free, err := admin.DiskFree(a.opts.DataDir); err == nil {
		fact.DiskFreeBytes = free
		fact.DiskFreeKnown = true
	}
	return fact, nil
}

func supportConnectivity(cfg config.Config) diagnostics.ConnectivityFact {
	return diagnostics.ConnectivityFact{
		RemoteAccess:          cfg.Plex.RemoteAccess,
		SecureConnections:     cfg.Plex.SecureConnections,
		CertificateConfigured: cfg.Plex.CertFile != "" || cfg.Plex.KeyFile != "",
		PublicPort:            cfg.Plex.PublicPort,
	}
}

func (a *App) supportLogs(q diagnostics.LogQuery) ([]diagnostics.LogRecord, bool, error) {
	if q.MaxLines <= 0 {
		q.MaxLines = 200
	}
	var out []diagnostics.LogRecord
	truncated := false
	add := func(rec diagnostics.LogRecord) {
		if !diagnostics.LogCorrelated(rec, q) {
			return
		}
		if len(out) >= q.MaxLines {
			truncated = true
			return
		}
		out = append(out, rec)
	}
	if a.logs != nil {
		for _, e := range a.logs.Entries(0, "", "") {
			add(diagnostics.LogRecord{Time: e.Time, Level: e.Level, Message: e.Message, Attrs: e.Attrs})
		}
	}
	path := filepath.Join(a.opts.DataDir, "logs", logfile.MainLogName)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, truncated, nil
		}
		return out, truncated, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out, truncated, err
	}
	const tail = 64 << 10
	skipPartial := false
	if info.Size() > tail {
		if _, err := f.Seek(info.Size()-tail, io.SeekStart); err != nil {
			return out, truncated, err
		}
		skipPartial = true
		truncated = true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 256<<10)
	for sc.Scan() {
		if skipPartial {
			skipPartial = false
			continue
		}
		rec, ok := diagnostics.ParseLogLine(sc.Text())
		if !ok {
			continue
		}
		add(rec)
	}
	if err := sc.Err(); err != nil {
		return out, true, err
	}
	return out, truncated, nil
}

func containerHint() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "yes"
	}
	if os.Getenv("container") != "" {
		return "yes"
	}
	if runtime.GOOS == "linux" {
		if raw, err := os.ReadFile("/proc/1/cgroup"); err == nil {
			for _, marker := range []string{"docker", "kubepods", "containerd", "podman", "lxc"} {
				if strings.Contains(string(raw), marker) {
					return "yes"
				}
			}
		}
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return "no"
	}
	return ""
}

func supportInstallType(container string) string {
	if container == "yes" {
		return "container"
	}
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	case "linux":
		if _, err := os.Stat("/etc/synoinfo.conf"); err == nil {
			return "synology"
		}
		if _, err := os.Stat("/etc/config/uLinux.conf"); err == nil {
			return "qnap"
		}
	}
	return "unknown"
}

func (a *App) supportViewer(userID int64) (diagnostics.ViewerFacts, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user, err := a.users.ActiveUser(ctx, userID)
	if err != nil {
		return diagnostics.ViewerFacts{}, err
	}
	v, err := a.frontends.supportViewer(ctx, user)
	if err != nil {
		return v, err
	}
	if a.personalize != nil {
		layout, e := a.personalize.HomeLayout(ctx)
		if e != nil {
			return v, e
		}
		add := func(layer string, pages []core.LayoutPage) {
			for _, p := range pages {
				for _, row := range p.Rows {
					if len(v.Layout) >= 256 {
						v.LayoutTruncated = true
						return
					}
					v.Layout = append(v.Layout, diagnostics.ViewerLayoutRow{Layer: layer, Section: p.Section, Row: row.Row, Status: row.Status, Slot: row.Slot})
				}
			}
		}
		add("global", layout.Global)
		for _, viewer := range layout.Viewers {
			if viewer.UserID == userID {
				add("viewer", viewer.Pages)
			}
		}
	}
	if a.state == nil {
		return v, nil
	}
	v.WatchSample, v.InProgress, v.Watched, v.WatchSampleTruncated, err = a.state.WatchSample(ctx, userID, 500)
	return v, err
}
