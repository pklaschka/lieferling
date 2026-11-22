// Package store is the core that both the command line and the app use: it
// joins the shared list of apps, what is installed here, and the forges
// (GitHub, Forgejo, Gitea) the releases come from.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/pklaschka/lieferling/internal/attest"
	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/install"
	"github.com/pklaschka/lieferling/internal/pick"
	"github.com/pklaschka/lieferling/internal/state"
	"github.com/pklaschka/lieferling/internal/verify"
)

// Attester checks build attestations: attest.Verifier in the app, a fake in
// tests.
type Attester interface {
	Verify(ctx context.Context, bundles [][]byte, want attest.Expect) (verify.Origin, error)
}

// sigstoreDir is where the Sigstore trust roots are cached, next to the icons.
func sigstoreDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Lieferling", "sigstore")
}

// Store does the work. Its zero value is not usable; use New.
type Store struct {
	ConfigPath string
	StatePath  string
	CacheDir   string // icons; "" keeps nothing between calls
	Forge      *forge.Client
	Installer  install.Installer
	// Attest checks build attestations; nil counts them as not checkable.
	Attest Attester
	GOOS   string
	GOARCH string

	mu sync.Mutex // serializes installs and state writes
}

// New returns a store with the default paths, tokens and installer.
func New() *Store {
	cfg, st := config.DefaultPath(), state.DefaultPath()
	// best effort: without it the list starts empty, which Add can fix
	_ = MigrateFromElappma(cfg, config.LegacyPath(), st, state.LegacyPath())
	return &Store{
		ConfigPath: cfg,
		StatePath:  st,
		CacheDir:   cacheDir(),
		Forge:      forge.New(),
		Installer:  install.New(install.DefaultOptions()),
		Attest:     attest.New(sigstoreDir()),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
	}
}

// NormalizeRepo accepts owner/name for GitHub, host/owner/name for a Forgejo
// or Gitea server, and their URLs (with or without .git). GitHub
// repositories are written owner/name, others host/owner/name.
func NormalizeRepo(s string) (string, error) {
	r, err := forge.ParseRepo(s)
	if err != nil {
		return "", err
	}
	return r.String(), nil
}

func validChannel(ch string) (string, error) {
	switch ch {
	case "", pick.Stable:
		return pick.Stable, nil
	case pick.Prerelease, "rc", "beta":
		return pick.Prerelease, nil
	}
	return "", fmt.Errorf("unknown channel %q: use stable or prerelease", ch)
}

// Config returns the shared configuration.
func (s *Store) Config() (config.Config, error) { return config.Load(s.ConfigPath) }
