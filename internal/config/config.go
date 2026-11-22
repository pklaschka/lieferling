// Package config holds the configuration shared by all computers: the list
// of apps and their channels. It lives in iCloud Drive on macOS, so every Mac
// signed in to the same iCloud account sees the same list. What is installed
// where stays local (package state).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/pklaschka/lieferling/internal/verify"
)

// App is one repository in the list.
type App struct {
	Repo    string `json:"repo"`    // owner/name
	Channel string `json:"channel"` // pick.Stable or pick.Prerelease
	// AppName is the bundle name learned at the first install (such as
	// "Feldspur"), so other computers can find a copy installed without Lieferling.
	AppName string `json:"appName,omitempty"`
	// Verify is this app's exception from Config.Verify: "", "standard",
	// "strict" or "off".
	Verify string `json:"verify,omitempty"`
	// BundleID and TeamID are the sender of the last package installed on any
	// computer. TeamID is "" for unsigned apps.
	BundleID string `json:"bundleId,omitempty"`
	TeamID   string `json:"teamId,omitempty"`
	// Workflow, when set, is the workflow that must have built this app's
	// packages (".github/workflows/release.yml"). GitHub only. It makes a
	// verified build attestation mandatory for this app.
	Workflow string `json:"workflow,omitempty"`
	// Digests maps "version/assetName" to "sha256:…" for the last few
	// versions, so every computer compares with what another one saw. A value
	// with two digests separated by a space means the computers disagree.
	Digests map[string]string `json:"digests,omitempty"`
}

// Config is the shared configuration file.
type Config struct {
	Version int   `json:"version"`
	Apps    []App `json:"apps"`
	// CheckIntervalHours is how often the app looks for updates (default 6).
	CheckIntervalHours int `json:"checkIntervalHours,omitempty"`
	// Verify is the customs policy for all apps: "" or "standard", or
	// "strict" (see package verify).
	Verify string `json:"verify,omitempty"`
}

// Find returns the app for repo (case-insensitive) or nil.
func (c *Config) Find(repo string) *App {
	for i := range c.Apps {
		if strings.EqualFold(c.Apps[i].Repo, repo) {
			return &c.Apps[i]
		}
	}
	return nil
}

// Interval returns CheckIntervalHours with its default.
func (c *Config) Interval() int {
	if c.CheckIntervalHours <= 0 {
		return 6
	}
	return c.CheckIntervalHours
}

// DefaultPath is LIEFERLING_CONFIG if set, else Lieferling/config.json in
// iCloud Drive on a Mac that has it, else in the user's configuration
// directory.
func DefaultPath() string {
	if p := os.Getenv("LIEFERLING_CONFIG"); p != "" {
		return p
	}
	if p := os.Getenv("ELAPPMA_CONFIG"); p != "" { // the old name
		return p
	}
	return pathIn("Lieferling")
}

// LegacyPath is where Lieferling kept the list while it was called Elappma.
func LegacyPath() string { return pathIn("Elappma") }

func pathIn(folder string) string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" && home != "" {
		icloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
		if st, err := os.Stat(icloud); err == nil && st.IsDir() {
			return filepath.Join(icloud, folder, "config.json")
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = home
	}
	return filepath.Join(dir, folder, "config.json")
}

// Load reads the configuration. A missing file is an empty configuration.
// Conflict copies that iCloud leaves beside the file ("config 2.json") are
// merged in, so an app added on two Macs at once is not lost.
func Load(path string) (Config, error) {
	cfg, err := read(path)
	if err != nil {
		return cfg, err
	}
	for _, p := range conflictCopies(path) {
		other, err := read(p)
		if err != nil {
			continue
		}
		merge(&cfg, other)
	}
	return cfg, nil
}

// merge folds a conflict copy into dst. Apps only in the copy are added. For
// apps in both, the main file wins, except: digests are unioned (two
// different digests for one key are kept together, which marks the version
// as contested), an empty sender is filled from the copy as a pair, and the
// stricter policy wins.
func merge(dst *Config, other Config) {
	dst.Verify = verify.Stricter(dst.Verify, other.Verify)
	for _, o := range other.Apps {
		a := dst.Find(o.Repo)
		if a == nil {
			dst.Apps = append(dst.Apps, o)
			continue
		}
		if a.AppName == "" {
			a.AppName = o.AppName
		}
		if a.BundleID == "" && a.TeamID == "" {
			a.BundleID, a.TeamID = o.BundleID, o.TeamID
		}
		a.Verify = verify.Stricter(a.Verify, o.Verify)
		if a.Workflow == "" {
			a.Workflow = o.Workflow // a pin never vanishes in a merge
		}
		for k, v := range o.Digests {
			if a.Digests == nil {
				a.Digests = map[string]string{}
			}
			a.Digests[k] = verify.Merge(a.Digests[k], v)
		}
	}
}

// Update reads the newest file, lets fn change it, and writes it back. Any
// conflict copies are folded in and removed.
func Update(path string, fn func(*Config) error) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(&cfg); err != nil {
		return err
	}
	if err := write(path, cfg); err != nil {
		return err
	}
	for _, p := range conflictCopies(path) {
		_ = os.Remove(p)
	}
	return nil
}

func read(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Version: 1}, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return cfg, nil
}

func write(path string, cfg Config) error {
	cfg.Version = 1
	sort.SliceStable(cfg.Apps, func(i, j int) bool {
		return strings.ToLower(cfg.Apps[i].Repo) < strings.ToLower(cfg.Apps[j].Repo)
	})
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'))
}

// WriteFileAtomic writes through a temporary file and a rename, so a reader
// (or iCloud) never sees half a file.
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// conflictCopies finds "config 2.json", "config 3.json" ... beside path.
func conflictCopies(path string) []string {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	matches, _ := filepath.Glob(stem + " *" + ext)
	return matches
}
