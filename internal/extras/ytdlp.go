package extras

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/egress"
)

// PinnedYTDLPVersion is the default official yt-dlp standalone release version
// installed when the owner clicks Install yt-dlp.
const PinnedYTDLPVersion = "2026.10.26"

const defaultYTDLPReleaseURL = "https://github.com/yt-dlp/yt-dlp/releases"

const maxYTDLPBinarySize int64 = 96 << 20

type ytdlpPlatformAsset struct {
	Asset  string
	Binary string
	SHA256 string
}

// Pinned SHA-256 digests from https://github.com/yt-dlp/yt-dlp/releases/download/2026.10.26/SHA2-256SUMS.
var pinnedYTDLPAssets = map[string]ytdlpPlatformAsset{
	"darwin/amd64": {
		Asset:  "yt-dlp_macos",
		Binary: "yt-dlp",
		SHA256: "0f922c15ccc0b83eefa1c8bc9104e434318c4bf16e408e1ee93b7745a6efe0aa",
	},
	"darwin/arm64": {
		Asset:  "yt-dlp_macos",
		Binary: "yt-dlp",
		SHA256: "0f922c15ccc0b83eefa1c8bc9104e434318c4bf16e408e1ee93b7745a6efe0aa",
	},
	"linux/amd64": {
		Asset:  "yt-dlp_linux",
		Binary: "yt-dlp",
		SHA256: "767ba9dce7052aa196f8b12652f433d76de128ba168966fa84b6394d5e6c6795",
	},
	"linux/arm64": {
		Asset:  "yt-dlp_linux_aarch64",
		Binary: "yt-dlp",
		SHA256: "e87e9533e89fe5889abaa21f5114dfe7756d6aa6219b83551add04d48b18e483",
	},
	"windows/amd64": {
		Asset:  "yt-dlp.exe",
		Binary: "yt-dlp.exe",
		SHA256: "f5389d3698ecde4ef06f4e6b1ce0294fe9ac1396228c8990b8d3416d6d177a7d",
	},
	"windows/arm64": {
		Asset:  "yt-dlp_arm64.exe",
		Binary: "yt-dlp.exe",
		SHA256: "0066dbac3c124028e2dcae2a197fccee33cd91f456313d24f7af89fd54590e98",
	},
}

// DownloaderStatus describes the active yt-dlp-compatible command and managed
// binary installation state for the console.
type DownloaderStatus struct {
	Available     bool   `json:"available"`
	Source        string `json:"source"` // "managed", "path", "configured", "none"
	Version       string `json:"version,omitempty"`
	PinnedVersion string `json:"pinnedVersion"`
	CanInstall    bool   `json:"canInstall"`
	CanUpdate     bool   `json:"canUpdate"`
}

// DownloaderOptions configures managed yt-dlp discovery, installation and media downloads.
type DownloaderOptions struct {
	ToolsDir   string
	Config     func() config.Extras
	Runner     CommandRunner
	LookPath   func(string) (string, error)
	FFmpeg     func() string
	ReleaseURL string
	Client     *http.Client
	GOOS       string
	GOARCH     string
	Now        func() time.Time
}

type managedMetadata struct {
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Asset     string `json:"asset"`
	UpdatedAt int64  `json:"updatedAt"`
}

type verifiedBinaryCache struct {
	path    string
	size    int64
	modNano int64
	sha256  string
	version string
}

// Downloader manages the owner-initiated yt-dlp binary in Ether's data dir and
// runs bounded single-video downloads using the shared Extras resolver config.
type Downloader struct {
	toolsDir   string
	cfgFn      func() config.Extras
	runner     CommandRunner
	look       func(string) (string, error)
	ffmpeg     func() string
	releaseURL string
	client     *http.Client
	goos       string
	goarch     string
	now        func() time.Time

	installMu sync.Mutex
	verifyMu  sync.Mutex
	verified  verifiedBinaryCache
}

// NewDownloader builds a Downloader sharing the Extras resolver configuration.
func NewDownloader(o DownloaderOptions) *Downloader {
	if o.Config == nil {
		o.Config = func() config.Extras { return config.Extras{} }
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.ReleaseURL == "" {
		o.ReleaseURL = defaultYTDLPReleaseURL
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Downloader{
		toolsDir:   o.ToolsDir,
		cfgFn:      o.Config,
		runner:     o.Runner,
		look:       o.LookPath,
		ffmpeg:     o.FFmpeg,
		releaseURL: strings.TrimRight(o.ReleaseURL, "/"),
		client:     o.Client,
		goos:       o.GOOS,
		goarch:     o.GOARCH,
		now:        o.Now,
	}
}

func (d *Downloader) platformAsset() (ytdlpPlatformAsset, bool) {
	a, ok := pinnedYTDLPAssets[d.goos+"/"+d.goarch]
	return a, ok
}

func (d *Downloader) managedDir() string {
	if d.toolsDir == "" {
		return ""
	}
	return filepath.Join(d.toolsDir, "yt-dlp")
}

// VerifiedManagedBinary returns the path and version of the managed yt-dlp
// binary only when its metadata and SHA-256 digest are verified. An unverified
// or modified binary is never returned or executed.
func (d *Downloader) VerifiedManagedBinary() (string, string, bool) {
	dir := d.managedDir()
	spec, ok := d.platformAsset()
	if dir == "" || !ok {
		return "", "", false
	}
	metaPath := filepath.Join(dir, ".metadata.json")
	rawMeta, err := os.ReadFile(metaPath)
	if err != nil {
		return "", "", false
	}
	var meta managedMetadata
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		return "", "", false
	}
	wantSum := strings.ToLower(strings.TrimSpace(meta.SHA256))
	if !validHexSHA256(wantSum) || meta.Version == "" {
		return "", "", false
	}
	if meta.Version == PinnedYTDLPVersion && wantSum != spec.SHA256 {
		return "", "", false
	}
	binPath := filepath.Join(dir, spec.Binary)
	st, err := os.Lstat(binPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxYTDLPBinarySize {
		return "", "", false
	}
	d.verifyMu.Lock()
	defer d.verifyMu.Unlock()
	if d.verified.path == binPath && d.verified.size == st.Size() && d.verified.modNano == st.ModTime().UnixNano() && d.verified.sha256 == wantSum {
		return binPath, meta.Version, true
	}
	gotSum, err := fileSHA256(binPath, maxYTDLPBinarySize)
	if err != nil || gotSum != wantSum {
		return "", "", false
	}
	d.verified = verifiedBinaryCache{
		path:    binPath,
		size:    st.Size(),
		modNano: st.ModTime().UnixNano(),
		sha256:  gotSum,
		version: meta.Version,
	}
	return binPath, meta.Version, true
}

// EffectiveCommand resolves the command to run:
// 1. Owner-configured config.Extras.ResolverCommand if non-empty;
// 2. Verified managed yt-dlp binary in Ether's data dir;
// 3. Default "yt-dlp" on PATH.
func (d *Downloader) EffectiveCommand() (cmd string, source string, version string, ok bool) {
	cfg := d.cfgFn()
	if custom := strings.TrimSpace(cfg.ResolverCommand); custom != "" {
		if resolved, err := d.look(custom); err == nil && resolved != "" {
			return resolved, "configured", "", true
		}
		_, err := d.look(custom)
		return custom, "configured", "", err == nil
	}
	if path, ver, verified := d.VerifiedManagedBinary(); verified {
		return path, "managed", ver, true
	}
	if resolved, err := d.look(DefaultResolverCommand); err == nil {
		if resolved == "" {
			resolved = DefaultResolverCommand
		}
		return resolved, "path", "", true
	}
	return "", "none", "", false
}

// Status reports the current resolver/downloader state for the console.
func (d *Downloader) Status() DownloaderStatus {
	_, supported := d.platformAsset()
	canManage := supported && d.managedDir() != ""
	_, source, version, ok := d.EffectiveCommand()
	switch source {
	case "configured":
		return DownloaderStatus{
			Available:     ok,
			Source:        "configured",
			PinnedVersion: PinnedYTDLPVersion,
			CanInstall:    false,
			CanUpdate:     false,
		}
	case "managed":
		return DownloaderStatus{
			Available:     true,
			Source:        "managed",
			Version:       version,
			PinnedVersion: PinnedYTDLPVersion,
			CanInstall:    canManage,
			CanUpdate:     canManage,
		}
	case "path":
		return DownloaderStatus{
			Available:     true,
			Source:        "path",
			PinnedVersion: PinnedYTDLPVersion,
			CanInstall:    canManage,
			CanUpdate:     false,
		}
	default:
		return DownloaderStatus{
			Available:     false,
			Source:        "none",
			PinnedVersion: PinnedYTDLPVersion,
			CanInstall:    canManage,
			CanUpdate:     false,
		}
	}
}

// Install downloads or updates the official standalone yt-dlp release binary
// into Ether's data dir after verifying its SHA-256 against the release's
// published SHA2-256SUMS (and the pinned digest for PinnedYTDLPVersion).
func (d *Downloader) Install(ctx context.Context, update bool) (DownloaderStatus, error) {
	if strings.TrimSpace(d.cfgFn().ResolverCommand) != "" {
		return d.Status(), errors.New("a custom resolver command is already configured in Playback settings")
	}
	spec, ok := d.platformAsset()
	if !ok {
		return d.Status(), fmt.Errorf("managed yt-dlp install is not supported on %s/%s", d.goos, d.goarch)
	}
	dir := d.managedDir()
	if dir == "" {
		return d.Status(), errors.New("managed tools directory is not configured")
	}

	d.installMu.Lock()
	defer d.installMu.Unlock()

	if !update {
		if _, _, verified := d.VerifiedManagedBinary(); verified {
			return d.Status(), nil
		}
	}

	client := d.httpClient()
	version := PinnedYTDLPVersion
	sumsURL := fmt.Sprintf("%s/download/%s/SHA2-256SUMS", d.releaseURL, version)
	if update {
		latestSumsURL := fmt.Sprintf("%s/latest/download/SHA2-256SUMS", d.releaseURL)
		sumsBody, finalURL, err := fetchReleaseBytes(ctx, client, latestSumsURL, 1<<20)
		if err == nil {
			if tag := extractTagFromReleaseURL(finalURL); tag != "" {
				version = tag
			}
			return d.installVerifiedVersion(ctx, client, dir, spec, version, sumsBody)
		}
	}

	sumsBody, _, err := fetchReleaseBytes(ctx, client, sumsURL, 1<<20)
	if err != nil {
		return d.Status(), fmt.Errorf("could not download yt-dlp checksums: %w", err)
	}
	return d.installVerifiedVersion(ctx, client, dir, spec, version, sumsBody)
}

func (d *Downloader) installVerifiedVersion(ctx context.Context, client *http.Client, dir string, spec ytdlpPlatformAsset, version string, sumsBody []byte) (DownloaderStatus, error) {
	publishedSum, ok := parseSHA256Sums(sumsBody, spec.Asset)
	if !ok {
		return d.Status(), fmt.Errorf("yt-dlp release %s checksums did not include %s", version, spec.Asset)
	}
	if version == PinnedYTDLPVersion && publishedSum != spec.SHA256 {
		return d.Status(), errors.New("yt-dlp checksum did not match the pinned release digest; refusing to install")
	}

	// If the currently installed binary is already verified with the exact same digest, keep it.
	if _, curVer, verified := d.VerifiedManagedBinary(); verified {
		d.verifyMu.Lock()
		sameSum := d.verified.sha256 == publishedSum
		d.verifyMu.Unlock()
		if sameSum && curVer == version {
			return d.Status(), nil
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return d.Status(), err
	}
	stage, err := os.MkdirTemp(dir, ".yt-dlp-stage-")
	if err != nil {
		return d.Status(), err
	}
	defer os.RemoveAll(stage)

	binURL := fmt.Sprintf("%s/download/%s/%s", d.releaseURL, version, spec.Asset)
	stagedBin := filepath.Join(stage, spec.Binary)
	gotSum, err := downloadToFileAndHash(ctx, client, binURL, stagedBin, maxYTDLPBinarySize)
	if err != nil {
		return d.Status(), err
	}
	if gotSum != publishedSum {
		return d.Status(), errors.New("yt-dlp binary SHA-256 verification failed; unverified file was discarded")
	}
	if err := ctx.Err(); err != nil {
		return d.Status(), err
	}
	if err := os.Chmod(stagedBin, 0o755); err != nil {
		return d.Status(), err
	}
	meta := managedMetadata{
		Version:   version,
		SHA256:    gotSum,
		Asset:     spec.Asset,
		UpdatedAt: d.now().Unix(),
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return d.Status(), err
	}
	stagedMeta := filepath.Join(stage, ".metadata.json")
	if err := os.WriteFile(stagedMeta, append(metaBytes, '\n'), 0o600); err != nil {
		return d.Status(), err
	}

	finalBin := filepath.Join(dir, spec.Binary)
	finalMeta := filepath.Join(dir, ".metadata.json")
	if err := os.Rename(stagedBin, finalBin); err != nil {
		return d.Status(), err
	}
	if err := os.Rename(stagedMeta, finalMeta); err != nil {
		_ = os.Remove(finalBin)
		return d.Status(), err
	}
	d.verifyMu.Lock()
	d.verified = verifiedBinaryCache{}
	d.verifyMu.Unlock()

	if _, _, verified := d.VerifiedManagedBinary(); !verified {
		return d.Status(), errors.New("installed yt-dlp binary failed post-install verification")
	}
	return d.Status(), nil
}

func (d *Downloader) httpClient() *http.Client {
	if d.client != nil {
		return d.client
	}
	c := egress.Client(egress.CapDirect, 5*time.Minute)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return errors.New("unsafe yt-dlp download redirect")
		}
		return nil
	}
	return c
}

func fetchReleaseBytes(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Ether-YTDLP-Installer/1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", errors.New("network error while contacting yt-dlp release server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("release server returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", errors.New("network read interrupted")
	}
	if int64(len(b)) > maxBytes || len(b) == 0 {
		return nil, "", errors.New("invalid release metadata size")
	}
	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return b, finalURL, nil
}

func downloadToFileAndHash(ctx context.Context, client *http.Client, rawURL, dst string, maxBytes int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Ether-YTDLP-Installer/1")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("network error while downloading yt-dlp binary")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("yt-dlp download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return "", errors.New("yt-dlp binary exceeds size limit")
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return "", errors.New("yt-dlp download interrupted")
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n <= 0 || n > maxBytes {
		return "", errors.New("yt-dlp binary size invalid")
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return "", errors.New("yt-dlp download was truncated")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func parseSHA256Sums(body []byte, asset string) (string, bool) {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if name == asset && validHexSHA256(sum) {
			return sum, true
		}
	}
	return "", false
}

func extractTagFromReleaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "download" && parts[i+1] != "" && parts[i+1] != "SHA2-256SUMS" {
			return parts[i+1]
		}
	}
	return ""
}

func validHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func fileSHA256(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxBytes+1))
	if err != nil || n <= 0 || n > maxBytes {
		return "", fmt.Errorf("invalid file size")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadRequest describes a bounded single-video or audio download job.
type DownloadRequest struct {
	URL         string
	Kind        string // "video" or "audio"
	Reel        bool
	MaxBytes    int64
	MaxDuration time.Duration
	OutputDir   string
	Progress    func(float64)
}

// DownloadResult holds either the local file downloaded by yt-dlp or a
// resolved direct stream URL from a custom resolver command, along with
// provenance metadata.
type DownloadResult struct {
	Path        string
	ResolvedURL string
	Headers     map[string]string
	Title       string
	Attribution string
	Duration    time.Duration
}

var extractorHostSuffixes = []string{
	"youtube.com",
	"youtu.be",
	"youtube-nocookie.com",
	"vimeo.com",
	"dailymotion.com",
	"dai.ly",
	"twitch.tv",
	"tiktok.com",
	"streamable.com",
	"soundcloud.com",
	"bandcamp.com",
	"bilibili.com",
	"b23.tv",
	"nicovideo.jp",
}

// IsExtractorSiteURL reports whether raw points to a known video/audio
// platform watch page that requires a yt-dlp-compatible extractor rather than
// a direct file GET.
func IsExtractorSiteURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, suffix := range extractorHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func isPlaylistOnlyURL(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(strings.TrimSuffix(u.Path, "/"))
	if host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") {
		if path == "/playlist" || strings.HasPrefix(path, "/playlist/") {
			return true
		}
		if path == "/watch" && u.Query().Get("v") == "" && u.Query().Get("list") != "" {
			return true
		}
		for _, prefix := range []string{"/@", "/channel/", "/c/", "/user/"} {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
}

var progressPercentRe = regexp.MustCompile(`\[download\]\s+([0-9]+(?:\.[0-9]+)?)%`)

// Download resolves and downloads a single video or audio clip from a page URL
// using the configured yt-dlp-compatible command without a shell.
func (d *Downloader) Download(ctx context.Context, req DownloadRequest) (DownloadResult, error) {
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return DownloadResult{}, errors.New("a public HTTP or HTTPS link is required")
	}
	if isPlaylistOnlyURL(u) {
		return DownloadResult{}, errors.New("playlists are not supported; paste a link to a single video")
	}
	cmd, _, _, ok := d.EffectiveCommand()
	if !ok {
		return DownloadResult{}, errors.New("yt-dlp is required to import from video sites; click Install yt-dlp to set it up")
	}
	cfg := d.cfgFn()
	timeout := cfg.ResolverTimeoutOr(8 * time.Minute)
	if timeout < 2*time.Minute && len(cfg.ResolverArgs) == 0 {
		timeout = 8 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	outTemplate := filepath.Join(req.OutputDir, "media.%(ext)s")
	var args []string
	if len(cfg.ResolverArgs) > 0 {
		sub := strings.NewReplacer(
			"{url}", req.URL,
			"{output}", outTemplate,
			"{site}", u.Hostname(),
			"{key}", "",
			"{id}", "",
		)
		args = make([]string, len(cfg.ResolverArgs))
		for i, a := range cfg.ResolverArgs {
			args[i] = sub.Replace(a)
		}
	} else {
		maxMB := max(1, req.MaxBytes/(1<<20))
		maxSec := int(req.MaxDuration.Seconds())
		filter := fmt.Sprintf("!playlist_count & duration <=? %d & !is_live", maxSec)
		format := "b[ext=mp4][vcodec^=avc1][acodec^=mp4a][height<=1080]/b[ext=mp4][vcodec^=avc1][height<=1080]/b[ext=mp4]/bv*[vcodec^=avc1][height<=1080]+ba[acodec^=mp4a]/b[height<=1080]/b"
		if req.Kind == "audio" {
			format = "ba[ext=m4a]/ba[acodec^=mp4a]/ba/b"
		}
		args = []string{
			"--no-playlist",
			"--playlist-items", "1",
			"--no-cookies",
			"--no-cookies-from-browser",
			"--no-cache-dir",
			"--no-warnings",
			"--newline",
			"--progress",
			"--progress-delta", "1",
			"--print-json",
			"--max-filesize", fmt.Sprintf("%dM", maxMB),
			"--break-match-filters", filter,
			"-f", format,
		}
		if req.Kind != "audio" {
			args = append(args, "--merge-output-format", "mp4")
		}
		if d.ffmpeg != nil {
			if ff := strings.TrimSpace(d.ffmpeg()); ff != "" {
				args = append(args, "--ffmpeg-location", ff)
			}
		}
		args = append(args, "-o", outTemplate, "--", req.URL)
	}

	stdout, stderr, runErr := d.executeDownloadCommand(ctx, cmd, args, req.Progress)
	if ctx.Err() != nil {
		return DownloadResult{}, ctx.Err()
	}

	meta, hasMeta := parseYTDLPMetadata(stdout)
	if hasMeta {
		if meta.Type == "playlist" || meta.PlaylistCount > 1 {
			return DownloadResult{}, errors.New("playlists are not supported; paste a link to a single video")
		}
		if meta.IsLive {
			return DownloadResult{}, errors.New("live streams cannot be imported; choose a recorded video")
		}
		if meta.Duration > 0 && time.Duration(meta.Duration*float64(time.Second)) > req.MaxDuration {
			return DownloadResult{}, durationLimitError(req.MaxDuration, req.Reel)
		}
		if meta.Filesize > 0 && meta.Filesize > req.MaxBytes {
			return DownloadResult{}, sizeLimitError(req.MaxBytes, req.Reel)
		}
	}

	combined := strings.TrimSpace(string(stdout) + "\n" + string(stderr))
	if runErr != nil {
		return DownloadResult{}, classifyDownloadError(combined, req.MaxBytes, req.MaxDuration, req.Reel)
	}

	title := strings.TrimSpace(meta.Title)
	attribution := firstNonEmpty(meta.Uploader, meta.Channel, meta.Creator, meta.Artist)
	dur := time.Duration(meta.Duration * float64(time.Second))

	if path, ok, sizeErr := findDownloadedFile(req.OutputDir, req.MaxBytes, req.Reel); sizeErr != nil {
		return DownloadResult{}, sizeErr
	} else if ok {
		return DownloadResult{
			Path:        path,
			Title:       title,
			Attribution: attribution,
			Duration:    dur,
		}, nil
	}

	// If no file was written directly (e.g. a -g or JSON URL resolver command),
	// parse stdout as a resolved stream URL.
	if hasMeta && strings.TrimSpace(meta.URL) != "" {
		resolved, err := finishResolved(Resolved{URL: strings.TrimSpace(meta.URL), Headers: meta.Headers}, d.now())
		if err != nil {
			return DownloadResult{}, classifyDownloadError(combined, req.MaxBytes, req.MaxDuration, req.Reel)
		}
		return DownloadResult{
			ResolvedURL: resolved.URL,
			Headers:     resolved.Headers,
			Title:       title,
			Attribution: attribution,
			Duration:    dur,
		}, nil
	}
	if resolved, err := parseResolverOutput(stdout, d.now()); err == nil && resolved.URL != "" {
		return DownloadResult{
			ResolvedURL: resolved.URL,
			Headers:     resolved.Headers,
			Title:       title,
			Attribution: attribution,
			Duration:    dur,
		}, nil
	}

	return DownloadResult{}, classifyDownloadError(combined, req.MaxBytes, req.MaxDuration, req.Reel)
}

func (d *Downloader) executeDownloadCommand(ctx context.Context, name string, args []string, progress func(float64)) ([]byte, []byte, error) {
	if d.runner != nil {
		out, err := d.runner(ctx, name, args)
		if progress != nil && len(out) > 0 {
			for _, m := range progressPercentRe.FindAllSubmatch(out, -1) {
				if pct, pErr := strconv.ParseFloat(string(m[1]), 64); pErr == nil {
					progress(min(99, pct))
				}
			}
		}
		if err != nil {
			return out, []byte(err.Error()), err
		}
		return out, nil, nil
	}

	cmd := exec.CommandContext(ctx, name, args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	const maxJSONStdout = 2 << 20
	outBuf := &cappedBuffer{max: maxJSONStdout}
	errBuf := &cappedBuffer{max: maxResolverStderr * 2}

	var wg sync.WaitGroup
	var lastProgress time.Time
	var progressMu sync.Mutex
	emitProgress := func(line string) {
		if progress == nil {
			return
		}
		m := progressPercentRe.FindStringSubmatch(line)
		if len(m) != 2 {
			return
		}
		pct, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return
		}
		progressMu.Lock()
		shouldEmit := time.Since(lastProgress) >= 250*time.Millisecond || pct >= 99
		if shouldEmit {
			lastProgress = time.Now()
		}
		progressMu.Unlock()
		if shouldEmit {
			progress(min(99, pct))
		}
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		scan := bufio.NewScanner(stdoutPipe)
		scan.Buffer(make([]byte, 64<<10), maxJSONStdout)
		for scan.Scan() {
			line := scan.Text()
			if progressPercentRe.MatchString(line) {
				emitProgress(line)
				continue
			}
			_, _ = outBuf.Write(append([]byte(line), '\n'))
		}
	}()
	go func() {
		defer wg.Done()
		scan := bufio.NewScanner(stderrPipe)
		scan.Buffer(make([]byte, 16<<10), 64<<10)
		for scan.Scan() {
			line := scan.Text()
			if progressPercentRe.MatchString(line) {
				emitProgress(line)
				continue
			}
			_, _ = errBuf.Write(append([]byte(line), '\n'))
		}
	}()
	wg.Wait()
	runErr := cmd.Wait()
	return outBuf.buf.Bytes(), errBuf.buf.Bytes(), runErr
}

type ytdlpJSONMeta struct {
	Type          string            `json:"_type"`
	PlaylistCount int               `json:"playlist_count"`
	Title         string            `json:"title"`
	Uploader      string            `json:"uploader"`
	Channel       string            `json:"channel"`
	Creator       string            `json:"creator"`
	Artist        string            `json:"artist"`
	Duration      float64           `json:"duration"`
	Filesize      int64             `json:"filesize"`
	IsLive        bool              `json:"is_live"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
}

func parseYTDLPMetadata(stdout []byte) (ytdlpJSONMeta, bool) {
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("{")) {
			continue
		}
		var m ytdlpJSONMeta
		if err := json.Unmarshal(line, &m); err == nil {
			if m.Title != "" || m.URL != "" || m.Type != "" || m.Duration > 0 || m.IsLive || m.PlaylistCount > 0 {
				return m, true
			}
		}
	}
	return ytdlpJSONMeta{}, false
}

func findDownloadedFile(dir string, maxBytes int64, reel bool) (string, bool, error) {
	if dir == "" {
		return "", false, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if strings.HasPrefix(name, ".") ||
			strings.HasSuffix(lower, ".part") ||
			strings.HasSuffix(lower, ".ytdl") ||
			strings.HasSuffix(lower, ".temp") ||
			strings.HasSuffix(lower, ".json") {
			continue
		}
		full := filepath.Join(dir, name)
		st, err := os.Lstat(full)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st.Size() > maxBytes {
			return "", false, sizeLimitError(maxBytes, reel)
		}
		if st.Size() > 0 {
			return full, true, nil
		}
	}
	return "", false, nil
}

func durationLimitError(maxDuration time.Duration, reel bool) error {
	if reel || maxDuration >= time.Minute {
		mins := max(1, int(maxDuration.Minutes()))
		return fmt.Errorf("video is too long; reel clips can be at most %d minutes", mins)
	}
	secs := max(1, int(maxDuration.Seconds()))
	return fmt.Errorf("media is too long; library assets can be at most %d seconds", secs)
}

func sizeLimitError(maxBytes int64, reel bool) error {
	mb := max(1, maxBytes/(1<<20))
	if reel {
		return fmt.Errorf("video is too large; reel clips can be at most %d MB", mb)
	}
	return fmt.Errorf("media is too large; library assets can be at most %d MB", mb)
}

func classifyDownloadError(raw string, maxBytes int64, maxDuration time.Duration, reel bool) error {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "playlist_count"):
		return errors.New("playlists are not supported; paste a link to a single video")
	case strings.Contains(lower, "does not pass filter") && strings.Contains(lower, "duration"):
		return durationLimitError(maxDuration, reel)
	case strings.Contains(lower, "is_live") || strings.Contains(lower, "live event") || strings.Contains(lower, "premieres in"):
		return errors.New("live streams cannot be imported; choose a recorded video")
	case strings.Contains(lower, "larger than max-filesize") || strings.Contains(lower, "exceeds size"):
		return sizeLimitError(maxBytes, reel)
	case strings.Contains(lower, "sign in to confirm your age") ||
		strings.Contains(lower, "age-restricted") ||
		strings.Contains(lower, "confirm your age") ||
		strings.Contains(lower, "inappropriate for some users") ||
		strings.Contains(lower, "sign in to confirm") ||
		strings.Contains(lower, "login required") ||
		strings.Contains(lower, "members-only") ||
		strings.Contains(lower, "private video"):
		return errors.New("this video is age-restricted or requires sign-in and cannot be downloaded")
	case strings.Contains(lower, "unsupported url") ||
		strings.Contains(lower, "no suitable infoextractor") ||
		strings.Contains(lower, "is not a valid url"):
		return errors.New("this site is not supported; paste a YouTube link, supported video page or direct media URL")
	case strings.Contains(lower, "not available in your country") ||
		strings.Contains(lower, "geo-restricted") ||
		strings.Contains(lower, "video unavailable") ||
		strings.Contains(lower, "has been removed"):
		return errors.New("this video is unavailable or restricted in the server's region")
	case strings.Contains(lower, "unable to download webpage") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "name or service not known") ||
		strings.Contains(lower, "nodename nor servname") ||
		strings.Contains(lower, "temporary failure in name resolution") ||
		strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "tls handshake"):
		return errors.New("network error while downloading media; check the link and the server's internet connection")
	default:
		clean := sanitizeCommandOutput(raw)
		if clean != "" {
			return fmt.Errorf("could not download media from link: %s", clean)
		}
		return errors.New("could not download media from link")
	}
}

var (
	urlQueryRe    = regexp.MustCompile(`https?://[^\s"']+\?[^\s"']+`)
	secretTokenRe = regexp.MustCompile(`(?i)(cookie|authorization|bearer|token|session|key|sig|signature|expire)[=:\s]+[^\s"';&]+`)
)

// sanitizeCommandOutput strips URL query parameters, cookies and tokens so
// credentials never appear in logs or error messages.
func sanitizeCommandOutput(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	picked := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		picked = line
		break
	}
	if picked == "" {
		return ""
	}
	picked = strings.TrimPrefix(picked, "ERROR:")
	picked = strings.TrimSpace(picked)
	picked = urlQueryRe.ReplaceAllStringFunc(picked, func(m string) string {
		if idx := strings.IndexByte(m, '?'); idx > 0 {
			return m[:idx]
		}
		return m
	})
	picked = secretTokenRe.ReplaceAllString(picked, "$1=[redacted]")
	if len(picked) > 200 {
		picked = picked[:200]
	}
	return picked
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
