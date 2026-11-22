package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/golang/snappy"
)

// ErrNoAttestations is returned for a forge that has no build attestations
// (Forgejo, Gitea).
var ErrNoAttestations = errors.New("this forge offers no build attestations")

const (
	provenancePredicate = "https://slsa.dev/provenance/v1"
	maxAttestations     = 8 << 20 // the API answer
	maxBundle           = 4 << 20 // one bundle from bundle_url
)

var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Attestations returns the Sigstore bundles (as JSON) of the build provenance
// attestations GitHub holds for the file with this digest ("sha256:…") in
// owner/repo; nil when it holds none. Only the first page of 30 is read.
func (c *GitHub) Attestations(ctx context.Context, repo, digest string) ([][]byte, error) {
	digest = strings.ToLower(digest)
	if !sha256Digest.MatchString(digest) {
		return nil, fmt.Errorf("%s: not a SHA-256 digest: %q", repo, digest)
	}
	u := c.base() + "/repos/" + repo + "/attestations/" + digest + "?per_page=30&predicate_type=" + url.QueryEscape(provenancePredicate)
	resp, err := c.request(ctx, u, "application/vnd.github+json")
	if errors.Is(err, ErrNotFound) {
		return nil, nil // GitHub answers 404 when it holds none
	}
	if err != nil {
		return nil, fmt.Errorf("%s: attestations: %w", repo, err)
	}
	defer resp.Body.Close()
	var list struct {
		Attestations []struct {
			Bundle    json.RawMessage `json:"bundle"`
			BundleURL string          `json:"bundle_url"`
		} `json:"attestations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAttestations)).Decode(&list); err != nil {
		return nil, fmt.Errorf("%s: reading attestations: %w", repo, err)
	}
	var bundles [][]byte
	for _, a := range list.Attestations {
		if b := bytes.TrimSpace(a.Bundle); len(b) > 0 && !bytes.Equal(b, []byte("null")) {
			bundles = append(bundles, b)
			continue
		}
		// only https, except against a test server
		if a.BundleURL == "" || !strings.HasPrefix(a.BundleURL, "https://") && c.BaseURL == "" {
			continue
		}
		b, err := c.fetchBundle(ctx, a.BundleURL)
		if err != nil {
			return nil, fmt.Errorf("%s: attestation bundle: %w", repo, err)
		}
		bundles = append(bundles, b)
	}
	return bundles, nil
}

// fetchBundle reads a snappy-compressed bundle from bundle_url, without the
// token: it is another host.
func (c *GitHub) fetchBundle(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusError("GitHub", resp); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBundle+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBundle {
		return nil, errors.New("bundle too large")
	}
	if n, err := snappy.DecodedLen(raw); err != nil || n > maxBundle {
		return nil, fmt.Errorf("bundle is not snappy-compressed JSON of a sane size")
	}
	return snappy.Decode(nil, raw)
}

// Attestations is GitHub.Attestations for GitHub repositories and
// ErrNoAttestations for all others.
func (c *Client) Attestations(ctx context.Context, repo, digest string) ([][]byte, error) {
	r, err := ParseRepo(repo)
	if err != nil {
		return nil, err
	}
	if r.Host != "" {
		return nil, ErrNoAttestations
	}
	return c.GitHub.Attestations(ctx, r.Path(), digest)
}
