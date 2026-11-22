package attest

import (
	"context"
	_ "embed"
	"net/http"
	"path/filepath"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

// githubRoot is the TUF root of GitHub's own Sigstore instance, which signs
// attestations of private repositories. Copied from the GitHub CLI (MIT),
// pkg/cmd/attestation/verification/embed/tuf-repo.github.com/root.json;
// refresh it when sigstore-go is updated.
//
//go:embed tuf/github-root.json
var githubRoot []byte

const githubMirror = "https://tuf-repo.github.com"

// rootLifetime is how long a loaded trust root is used before it is loaded
// again: the window app runs for weeks, and Sigstore rotates keys.
const rootLifetime = 24 * time.Hour

// fetchTimeout bounds loading a trust root, so a firewall that drops packets
// to a TUF host ends as "not checkable" instead of stalling the install.
const fetchTimeout = 30 * time.Second

// material loads the trust root of one Sigstore instance, only when a bundle
// needs it (a Mac with only public apps never asks tuf-repo.github.com), and
// again after a day. When loading again fails, the old root stays in use.
// The TUF metadata is cached on disk for a day as well.
func (v *Verifier) material(ctx context.Context, org string) (root.TrustedMaterial, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	m, ok := v.roots[org]
	at, timed := v.loaded[org]
	if ok && (!timed || time.Since(at) < rootLifetime) {
		return m, nil // a root without a load time was set by a test and stays
	}
	fresh, err := v.load(ctx, org)
	if err != nil {
		if ok {
			// keep the old one and try again in an hour, not on every install
			v.loaded[org] = time.Now().Add(time.Hour - rootLifetime)
			return m, nil
		}
		return nil, err
	}
	if v.roots == nil {
		v.roots = map[string]root.TrustedMaterial{}
	}
	if v.loaded == nil {
		v.loaded = map[string]time.Time{}
	}
	v.roots[org], v.loaded[org] = fresh, time.Now()
	return fresh, nil
}

func (v *Verifier) load(ctx context.Context, org string) (root.TrustedMaterial, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	f := fetcher.NewDefaultFetcher()
	f.SetHTTPClient(&http.Client{Timeout: fetchTimeout})
	opts := tuf.DefaultOptions().WithContext(ctx).WithCacheValidity(1).WithFetcher(f)
	if v.CacheDir == "" {
		opts = opts.WithDisableLocalCache()
	}
	var m root.TrustedMaterial
	var err error
	switch org {
	case orgPublicGood:
		if v.CacheDir != "" {
			opts = opts.WithCachePath(filepath.Join(v.CacheDir, "public"))
		}
		m, err = root.FetchTrustedRootWithOptions(opts)
	default: // orgGitHub
		opts = opts.WithRoot(githubRoot).WithRepositoryBaseURL(githubMirror)
		if v.CacheDir != "" {
			opts = opts.WithCachePath(filepath.Join(v.CacheDir, "github"))
		}
		m, err = fetchGitHub(opts)
	}
	return m, err
}

func fetchGitHub(opts *tuf.Options) (root.TrustedMaterial, error) {
	c, err := tuf.New(opts)
	if err != nil {
		return nil, err
	}
	b, err := c.GetTarget("trusted_root.json")
	if err != nil {
		return nil, err
	}
	return root.NewTrustedRootFromJSON(b)
}
