package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Posters configures optional owner-supplied artwork. Templates are secrets:
// exported provider URLs can embed account configuration and credentials.
type PosterAccount struct {
	Enabled     bool   `json:"enabled"`
	BaseURL     string `json:"baseUrl"`
	APIKey      string `json:"apiKey"`
	URLTemplate string `json:"urlTemplate"`
	Profile     string `json:"profile"`
}
type PosterAccounts struct {
	PostersPlus   PosterAccount `json:"postersplus"`
	XRDB          PosterAccount `json:"xrdb"`
	BetterPosters PosterAccount `json:"betterposters"`
	RPDB          PosterAccount `json:"rpdb"`
	Custom        PosterAccount `json:"custom"`
}

func (p *PosterAccounts) Entries() map[string]*PosterAccount {
	return map[string]*PosterAccount{"postersplus": &p.PostersPlus, "xrdb": &p.XRDB, "betterposters": &p.BetterPosters, "rpdb": &p.RPDB, "custom": &p.Custom}
}

type Posters struct {
	OwnerEnabled *bool          `json:"ownerEnabled,omitempty"`
	AssetFolder  string         `json:"assetFolder,omitempty"`
	Accounts     PosterAccounts `json:"accounts"`
	Version      int            `json:"version,omitempty"`
	Enabled      bool           `json:"enabled"`
	Source       string         `json:"source"`
	BaseURL      string         `json:"baseUrl"`
	APIKey       string         `json:"apiKey"`
	URLTemplate  string         `json:"urlTemplate"`
	Profile      string         `json:"profile"`
}

// Normalize migrates the original single-provider settings without activating unused credentials.
func (p Posters) Normalize() Posters {
	if p.Version == 0 {
		if a := p.Accounts.Entries()[p.Source]; a != nil && p.Enabled {
			*a = PosterAccount{true, p.BaseURL, p.APIKey, p.URLTemplate, p.Profile}
		} else {
			p.Source = "tmdb"
		}
		p.Version = 1
		p.Enabled = false
		p.BaseURL, p.APIKey, p.URLTemplate, p.Profile = "", "", "", ""
	}
	return p
}
func (p Posters) Selected() Posters {
	p = p.Normalize()
	a := p.Accounts.Entries()[p.Source]
	if a == nil || !a.Enabled {
		return Posters{}
	}
	return Posters{Enabled: true, Source: p.Source, BaseURL: a.BaseURL, APIKey: a.APIKey, URLTemplate: a.URLTemplate, Profile: a.Profile}
}
func (p Posters) Validate() error {
	if p.AssetFolder != "" {
		if !filepath.IsAbs(p.AssetFolder) {
			return errors.New("choose an absolute folder")
		}
		resolved, err := filepath.EvalSymlinks(p.AssetFolder)
		if err != nil {
			return err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if !info.IsDir() || resolved != filepath.Clean(p.AssetFolder) {
			return errors.New("choose a folder without symbolic links")
		}
	}
	if p.Version > 0 {
		for source, a := range p.Accounts.Entries() {
			if err := (Posters{Enabled: a.Enabled, Source: source, BaseURL: a.BaseURL, APIKey: a.APIKey, URLTemplate: a.URLTemplate, Profile: a.Profile}).Validate(); err != nil {
				return err
			}
		}
		if p.Source != "" && p.Source != "tmdb" && p.Source != "owner" {
			a := p.Accounts.Entries()[p.Source]
			if a == nil || !a.Enabled {
				return errors.New("choose a configured poster source")
			}
		}
		return nil
	}
	if len(p.URLTemplate) > 8192 || len(p.APIKey) > 2048 || len(p.BaseURL) > 2048 || len(p.Profile) > 256 {
		return errors.New("poster settings exceed length limit")
	}
	if !p.Enabled {
		return nil
	}
	switch p.Source {
	case "rpdb":
		if p.APIKey == "" {
			return errors.New("RPDB requires an API key")
		}
	case "postersplus", "xrdb":
		u, e := url.Parse(p.BaseURL)
		if e != nil || !posterURL(u) || u.RawQuery != "" || strings.ContainsAny(p.BaseURL, "{}") {
			return errors.New("poster provider requires an HTTP(S) base URL without credentials or query")
		}
	case "betterposters", "custom":
		template := p.URLTemplate
		if template == "" && p.Source == "betterposters" {
			return nil
		}
		u, e := url.Parse(template)
		if e != nil || !posterURL(u) || strings.ContainsAny(u.Host, "{}") {
			return errors.New("poster template requires a fixed HTTP(S) host without user information")
		}
		if p.Source == "betterposters" && (u.Scheme != "https" || u.Host != "btttr.cc") {
			return errors.New("BetterPosters templates must use https://btttr.cc")
		}
		hasID := false
		for _, key := range []string{"imdb_id", "tmdb_id", "type", "api_key"} {
			token := "{" + key + "}"
			if strings.Contains(template, token) && strings.HasSuffix(key, "_id") {
				hasID = true
			}
			template = strings.ReplaceAll(template, token, "")
		}
		if !hasID || strings.ContainsAny(template, "{}") {
			return errors.New("use {imdb_id}, or {tmdb_id} with {type}; optional {api_key}")
		}
		if strings.Contains(p.URLTemplate, "{tmdb_id}") && !strings.Contains(p.URLTemplate, "{type}") {
			return errors.New("TMDB templates require {type} to distinguish movie and TV IDs")
		}
	default:
		return errors.New("choose a supported poster source")
	}
	return nil
}
func posterURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
