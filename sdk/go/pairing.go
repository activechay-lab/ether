package etherplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PairingPaths are the fixed Ether endpoints a sidecar uses to pair.
const (
	// PairExchangePath accepts a one-time code and returns a credential.
	PairExchangePath = "/ether/plugins/pair"
	// TunnelPath is the WebSocket endpoint a paired sidecar dials.
	TunnelPath = "/ether/plugins/tunnel"

	credentialFileMode os.FileMode = 0o600
	stateDirMode       os.FileMode = 0o700
)

// savedCredential is what the SDK writes to <state dir>/credential.
type savedCredential struct {
	Credential string `json:"credential"`
	URL        string `json:"url"`
}

// PairingInputs are the pairing settings read from options and the
// environment. Every SDK resolves them in the same order.
type PairingInputs struct {
	// URL is the server's http(s) base (from ETHER_URL).
	URL string
	// Code is a one-time pairing code (from ETHER_PAIRING_CODE).
	Code string
	// Credential is an explicit credential; it always wins.
	Credential string
	// StateDir overrides where the credential file is kept.
	StateDir string
}

// pairingInputsFromEnv reads the options and ETHER_* environment variables.
func pairingInputsFromEnv(opts ServeOptions) PairingInputs {
	in := PairingInputs{URL: strings.TrimSpace(opts.URL)}
	if in.URL == "" {
		in.URL = strings.TrimSpace(os.Getenv("ETHER_URL"))
	}
	in.Code = strings.TrimSpace(os.Getenv("ETHER_PAIRING_CODE"))
	in.Credential = strings.TrimSpace(opts.Credential)
	if in.Credential == "" {
		in.Credential = strings.TrimSpace(os.Getenv("ETHER_PLUGIN_CREDENTIAL"))
	}
	in.StateDir = strings.TrimSpace(opts.StateDir)
	if in.StateDir == "" {
		in.StateDir = strings.TrimSpace(os.Getenv("ETHER_PLUGIN_STATE_DIR"))
	}
	return in
}

// PairURL returns the absolute URL that exchanges a code for a credential.
func PairURL(base string) (string, error) {
	b, err := serverBase(base)
	if err != nil {
		return "", err
	}
	return b + PairExchangePath, nil
}

// TunnelURL returns the absolute ws(s) tunnel URL for a server base.
func TunnelURL(base string) (string, error) {
	b, err := serverBase(base)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(b)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = TunnelPath
	return u.String(), nil
}

// serverBase normalizes an http(s) base, rejecting anything else.
func serverBase(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("etherplugin: ETHER_URL is not set")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("etherplugin: ETHER_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("etherplugin: ETHER_URL must use http or https")
	}
	if u.Host == "" {
		return "", errors.New("etherplugin: ETHER_URL has no host")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// StateDir resolves where the credential file is kept: an explicit dir, else
// /data when it exists and is writable (a Docker volume), else the OS user
// config dir under ether-plugins/<plugin id>/.
func StateDir(explicit, pluginID string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, nil
	}
	if info, err := os.Stat("/data"); err == nil && info.IsDir() && writableDir("/data") {
		return "/data", nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("etherplugin: no state directory available: %w", err)
	}
	return filepath.Join(base, "ether-plugins", pluginID), nil
}

func writableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".ether-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// resolveCredential applies the startup order: explicit credential, then the
// saved file, then a one-time pairing code (which is exchanged and saved). It
// returns whether the credential came from the file, so a later 401 can clear
// the file. It never logs the credential or the code.
func resolveCredential(ctx context.Context, in PairingInputs, pluginID string) (credential string, fromFile bool, err error) {
	dir, err := StateDir(in.StateDir, pluginID)
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, "credential")
	if in.Credential != "" {
		return in.Credential, false, nil
	}
	if saved, ok := readSavedCredential(path); ok {
		return saved.Credential, true, nil
	}
	if in.URL == "" {
		return "", false, noCredentialError()
	}
	if in.Code != "" {
		cred, err := exchangeCode(ctx, in.URL, in.Code, nil)
		if err != nil {
			return "", false, err
		}
		if err := writeSavedCredential(path, dir, savedCredential{Credential: cred, URL: in.URL}); err != nil {
			return "", false, err
		}
		return cred, true, nil
	}
	return "", false, noCredentialError()
}

func noCredentialError() error {
	return errors.New("No credential: create a pairing code in Ether (Plugins > this plugin > Connect a sidecar) and set ETHER_PAIRING_CODE.")
}

func readSavedCredential(path string) (savedCredential, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return savedCredential{}, false
	}
	var saved savedCredential
	if json.Unmarshal(raw, &saved) != nil || saved.Credential == "" {
		return savedCredential{}, false
	}
	return saved, true
}

func writeSavedCredential(path, dir string, saved savedCredential) error {
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("etherplugin: cannot create state directory: %w", err)
	}
	if err := os.Chmod(dir, stateDirMode); err != nil {
		return fmt.Errorf("etherplugin: cannot secure state directory: %w", err)
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credential-*")
	if err != nil {
		return fmt.Errorf("etherplugin: cannot write credential: %w", err)
	}
	name := tmp.Name()
	if err := os.Chmod(name, credentialFileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func clearSavedCredential(pluginID, explicitDir string) {
	dir, err := StateDir(explicitDir, pluginID)
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, "credential"))
}

// exchangeCode posts a one-time code to the server and returns the credential.
func exchangeCode(ctx context.Context, base, code string, client *http.Client) (string, error) {
	endpoint, err := PairURL(base)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return "", err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("etherplugin: pairing request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("etherplugin: pairing code was rejected (HTTP %d)", resp.StatusCode)
	}
	var out struct {
		Credential string `json:"credential"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Credential == "" {
		return "", errors.New("etherplugin: pairing response had no credential")
	}
	return out.Credential, nil
}
