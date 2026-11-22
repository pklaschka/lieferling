// Package attest checks build attestations: Sigstore bundles in which a
// GitHub Actions run says it built a file with a given digest. It is the only
// package that imports sigstore-go; internal/verify words and weighs the
// result, internal/store asks the forge for the bundles and calls Verify.
//
// See docs/adr/0003-attestation-signature-and-quarantine.md.
package attest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	sv "github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/pklaschka/lieferling/internal/verify"
)

// Expect is what an attestation must say to count.
type Expect struct {
	Digest   string // "sha256:<hex>" of the downloaded file
	Repo     string // owner/name on github.com
	Workflow string // normalized pin (verify.NormalizeWorkflow), or "" for any workflow of the repository
}

var (
	ErrTrust      = errors.New("the Sigstore trust root could not be loaded")
	ErrInvalid    = errors.New("no attestation with a valid signature for this file")
	ErrRepository = errors.New("the attestation names another repository")
	ErrWorkflow   = errors.New("the attestation names another workflow")
)

// Mismatch wraps ErrRepository or ErrWorkflow with what was expected and
// found.
type Mismatch struct {
	Kind      error
	Want, Got string
}

func (m *Mismatch) Error() string {
	return fmt.Sprintf("%v: expected %s, found %s", m.Kind, m.Want, m.Got)
}
func (m *Mismatch) Unwrap() error { return m.Kind }

const (
	issuer        = "https://token.actions.githubusercontent.com"
	provenance    = "https://slsa.dev/provenance/v1"
	orgPublicGood = "sigstore.dev"
	orgGitHub     = "GitHub, Inc."
)

// Verifier checks build attestations against Sigstore's public instance and
// GitHub's own (used for private repositories). Use New.
type Verifier struct {
	// CacheDir holds the TUF metadata; "" disables the local cache.
	CacheDir string

	mu     sync.Mutex
	roots  map[string]root.TrustedMaterial // by issuer organization; preset in tests
	loaded map[string]time.Time            // when each root was loaded
}

// New returns a Verifier that keeps its trust roots in cacheDir.
func New(cacheDir string) *Verifier {
	return &Verifier{CacheDir: cacheDir, roots: map[string]root.TrustedMaterial{}}
}

// Verify returns the origin of the first bundle that has a valid signature
// for want.Digest, was built in want.Repo and, when want.Workflow is set, by
// that workflow. It never returns an origin together with an error. Without
// a passing bundle the error is the most specific one seen: a *Mismatch for
// the workflow, then for the repository, then ErrInvalid. ErrTrust ends the
// call at once, since no bundle can be judged without the trust root.
func (v *Verifier) Verify(ctx context.Context, bundles [][]byte, want Expect) (verify.Origin, error) {
	algo, hexDigest, ok := strings.Cut(want.Digest, ":")
	raw, err := hex.DecodeString(hexDigest)
	if !ok || err != nil || len(raw) == 0 {
		return verify.Origin{}, fmt.Errorf("%w: digest %q", ErrInvalid, want.Digest)
	}
	var repoErr, workflowErr *Mismatch
	last := errors.New("no bundle")
	for _, data := range bundles {
		o, err := v.one(ctx, data, algo, raw, want)
		var m *Mismatch
		switch {
		case err == nil:
			return o, nil
		case errors.Is(err, ErrTrust):
			return verify.Origin{}, err
		case errors.As(err, &m) && errors.Is(m, ErrWorkflow):
			workflowErr = m
		case errors.As(err, &m):
			repoErr = m
		default:
			last = err
		}
	}
	switch {
	case workflowErr != nil:
		return verify.Origin{}, workflowErr
	case repoErr != nil:
		return verify.Origin{}, repoErr
	}
	return verify.Origin{}, fmt.Errorf("%w: %v", ErrInvalid, last)
}

func (v *Verifier) one(ctx context.Context, data []byte, algo string, digest []byte, want Expect) (verify.Origin, error) {
	var b bundle.Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return verify.Origin{}, fmt.Errorf("bundle: %w", err)
	}
	vc, err := b.VerificationContent()
	if err != nil {
		return verify.Origin{}, err
	}
	leaf := vc.Certificate()
	if leaf == nil || len(leaf.Issuer.Organization) == 0 {
		return verify.Origin{}, errors.New("the bundle holds no signing certificate")
	}
	org := leaf.Issuer.Organization[0]
	var opts []sv.VerifierOption
	switch org {
	case orgPublicGood:
		opts = []sv.VerifierOption{sv.WithSignedCertificateTimestamps(1), sv.WithTransparencyLog(1), sv.WithObserverTimestamps(1)}
	case orgGitHub:
		opts = []sv.VerifierOption{sv.WithSignedTimestamps(1)}
	default:
		return verify.Origin{}, fmt.Errorf("certificate from an unknown Sigstore instance %q", org)
	}
	material, err := v.material(ctx, org)
	if err != nil {
		return verify.Origin{}, fmt.Errorf("%w: %v", ErrTrust, err)
	}
	verifier, err := sv.NewVerifier(material, opts...)
	if err != nil {
		return verify.Origin{}, err
	}
	// The repository is compared below rather than in the sigstore policy:
	// sigstore-go compares extensions with case, GitHub does not, and the
	// mismatch deserves its own message.
	san, err := sv.NewSANMatcher("", `^https://github\.com/`)
	if err != nil {
		return verify.Origin{}, err
	}
	iss, err := sv.NewIssuerMatcher(issuer, "")
	if err != nil {
		return verify.Origin{}, err
	}
	id, err := sv.NewCertificateIdentity(san, iss, certificate.Extensions{})
	if err != nil {
		return verify.Origin{}, err
	}
	res, err := verifier.Verify(&b, sv.NewPolicy(sv.WithArtifactDigest(algo, digest), sv.WithCertificateIdentity(id)))
	if err != nil {
		return verify.Origin{}, err
	}
	if res.Statement == nil || res.Statement.PredicateType != provenance {
		return verify.Origin{}, errors.New("the attestation is no build provenance")
	}
	if res.Signature == nil || res.Signature.Certificate == nil {
		return verify.Origin{}, errors.New("the attestation names no build")
	}
	return originOf(res.Signature.Certificate, want)
}

// originOf compares a verified certificate with what is expected.
func originOf(c *certificate.Summary, want Expect) (verify.Origin, error) {
	repoURI := c.SourceRepositoryURI
	if !strings.EqualFold(repoURI, "https://github.com/"+want.Repo) {
		return verify.Origin{}, &Mismatch{ErrRepository, want.Repo, strings.TrimPrefix(repoURI, "https://github.com/")}
	}
	uri := c.BuildConfigURI
	if uri == "" {
		uri = c.BuildSignerURI
	}
	path, _, ok := workflowOf(uri, repoURI)
	if !ok {
		return verify.Origin{}, fmt.Errorf("the attestation names no workflow of %s", want.Repo)
	}
	if want.Workflow != "" && path != want.Workflow {
		return verify.Origin{}, &Mismatch{ErrWorkflow, want.Workflow, path}
	}
	o := verify.Origin{
		Repository: strings.TrimPrefix(repoURI, "https://github.com/"),
		Workflow:   path,
		Ref:        c.SourceRepositoryRef,
		Commit:     c.SourceRepositoryDigest,
	}
	if signer := signerOf(c.SubjectAlternativeName); signer != "" && !strings.EqualFold(signer, o.Repository+"/"+path) {
		o.Signer = signer
	}
	return o, nil
}

// signerOf is the SAN without "https://github.com/" and without its "@ref".
func signerOf(san string) string {
	s, ok := strings.CutPrefix(san, "https://github.com/")
	if !ok {
		return ""
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[:i]
	}
	return s
}

// workflowOf splits "https://github.com/o/r/.github/workflows/release.yml@refs/heads/main"
// (the repository URL compared without case) into the workflow path and the
// ref. A workflow file name holds no "/", so the path ends at the first "@"
// after ".github/workflows/".
func workflowOf(uri, repoURI string) (path, ref string, ok bool) {
	if len(uri) <= len(repoURI)+1 || !strings.EqualFold(uri[:len(repoURI)], repoURI) || uri[len(repoURI)] != '/' {
		return "", "", false
	}
	rest := uri[len(repoURI)+1:]
	const dir = ".github/workflows/"
	if !strings.HasPrefix(rest, dir) {
		return "", "", false
	}
	file, ref, _ := strings.Cut(rest[len(dir):], "@")
	if file == "" || strings.Contains(file, "/") {
		return "", "", false
	}
	return dir + file, ref, true
}
