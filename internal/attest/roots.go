package attest

import (
	"context"
	_ "embed"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

// githubRoot is the TUF root of GitHub's own Sigstore instance, which signs
// attestations of private repositories. Copied from the GitHub CLI (MIT),
// pkg/cmd/attestation/verification/embed/tuf-repo.github.com/root.json;
// refresh it when sigstore-go is updated.
//
//go:embed tuf/github-root.json
var githubRoot []byte

const githubMirror = "https://tuf-repo.github.com"

// material loads the trust root of one Sigstore instance, at most once per
// process and only when a bundle needs it: a Mac with only public apps never
// asks tuf-repo.github.com. The TUF metadata is cached for a day.
func (v *Verifier) material(ctx context.Context, org string) (root.TrustedMaterial, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if m, ok := v.roots[org]; ok {
		return m, nil
	}
	opts := tuf.DefaultOptions().WithContext(ctx).WithCacheValidity(1)
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
	if err != nil {
		return nil, err
	}
	if v.roots == nil {
		v.roots = map[string]root.TrustedMaterial{}
	}
	v.roots[org] = m
	return m, nil
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
