package attest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// fixtures: see testdata/README.md
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// offline is a Verifier that knows only the fixture trust root of the public
// instance and never touches the network.
func offline(t *testing.T) *Verifier {
	t.Helper()
	tr, err := root.NewTrustedRootFromJSON(fixture(t, "trusted-root-public-good.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Verifier{roots: map[string]root.TrustedMaterial{orgPublicGood: tr}}
}

const sigstoreJS = "sha512:90f223f992e4c88dd068cd2a5fc57f9d2b30798343dd6e38f29c240e04ba090ef831f84490847c4e82b9232c78e8a258463b1e55c0f7469f730265008fa6633f"

func reusableDigest(t *testing.T) string {
	s := sha256.Sum256(fixture(t, "reusable-workflow-artifact"))
	return "sha256:" + hex.EncodeToString(s[:])
}

func TestVerifyPublicBundle(t *testing.T) {
	o, err := offline(t).Verify(context.Background(), [][]byte{fixture(t, "sigstore-js-2.1.0-bundle.json")}, Expect{Digest: sigstoreJS, Repo: "sigstore/sigstore-js"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", o)
	if o.Repository != "sigstore/sigstore-js" || o.Workflow != ".github/workflows/release.yml" || o.ShortRef() != "main" || len(o.Commit) != 40 || o.Signer != "" {
		t.Errorf("origin = %+v", o)
	}
}

func TestVerifyRejects(t *testing.T) {
	js := fixture(t, "sigstore-js-2.1.0-bundle.json")
	v := offline(t)
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		bundles [][]byte
		want    Expect
		kind    error
	}{
		{"wrong digest", [][]byte{js}, Expect{Digest: sigstoreJS[:len(sigstoreJS)-2] + "00", Repo: "sigstore/sigstore-js"}, ErrInvalid},
		{"another repository", [][]byte{js}, Expect{Digest: sigstoreJS, Repo: "evil/sigstore-js"}, ErrRepository},
		{"another workflow", [][]byte{js}, Expect{Digest: sigstoreJS, Repo: "sigstore/sigstore-js", Workflow: ".github/workflows/build.yml"}, ErrWorkflow},
		{"not JSON", [][]byte{[]byte("nope")}, Expect{Digest: sigstoreJS, Repo: "sigstore/sigstore-js"}, ErrInvalid},
		{"no bundles", nil, Expect{Digest: sigstoreJS, Repo: "sigstore/sigstore-js"}, ErrInvalid},
		{"malformed digest", [][]byte{js}, Expect{Digest: "sha256", Repo: "sigstore/sigstore-js"}, ErrInvalid},
		{"workflow beats a bad bundle", [][]byte{[]byte("{}"), js}, Expect{Digest: sigstoreJS, Repo: "sigstore/sigstore-js", Workflow: ".github/workflows/x.yml"}, ErrWorkflow},
	} {
		o, err := v.Verify(ctx, c.bundles, c.want)
		if !errors.Is(err, c.kind) {
			t.Errorf("%s: %v, want %v", c.name, err, c.kind)
		}
		if o.Repository != "" {
			t.Errorf("%s: an origin came back with the error: %+v", c.name, o)
		}
	}
	var m *Mismatch
	_, err := v.Verify(ctx, [][]byte{js}, Expect{Digest: sigstoreJS, Repo: "evil/sigstore-js"})
	if !errors.As(err, &m) || m.Want != "evil/sigstore-js" || m.Got != "sigstore/sigstore-js" {
		t.Errorf("mismatch = %+v", m)
	}
	if _, err := v.Verify(ctx, [][]byte{js}, Expect{Digest: sigstoreJS, Repo: "Sigstore/Sigstore-JS", Workflow: ".github/workflows/release.yml"}); err != nil {
		t.Errorf("repository in another case, pinned: %v", err)
	}
}

func TestVerifyWrongTrustRoot(t *testing.T) {
	// a trust root that holds nothing the bundle could chain to
	v := &Verifier{roots: map[string]root.TrustedMaterial{orgPublicGood: &root.BaseTrustedMaterial{}}}
	_, err := v.Verify(context.Background(), [][]byte{fixture(t, "sigstore-js-2.1.0-bundle.json")}, Expect{Digest: sigstoreJS, Repo: "sigstore/sigstore-js"})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestReusableWorkflow(t *testing.T) {
	o, err := offline(t).Verify(context.Background(), [][]byte{fixture(t, "reusable-workflow-attestation.sigstore.json")}, Expect{Digest: reusableDigest(t), Repo: "malancas/attest-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Repository != "malancas/attest-demo" || o.Workflow != ".github/workflows/shared.yml" || o.Signer != "github/artifact-attestations-workflows/.github/workflows/attest.yml" {
		t.Errorf("origin = %+v", o)
	}
	// the reusable workflow's repository is not the app's
	if _, err := offline(t).Verify(context.Background(), [][]byte{fixture(t, "reusable-workflow-attestation.sigstore.json")}, Expect{Digest: reusableDigest(t), Repo: "github/artifact-attestations-workflows"}); !errors.Is(err, ErrRepository) {
		t.Errorf("signer's repository accepted: %v", err)
	}
}

func TestWorkflowOf(t *testing.T) {
	const repo = "https://github.com/o/r"
	for _, c := range []struct {
		uri, path, ref string
		ok             bool
	}{
		{repo + "/.github/workflows/release.yml@refs/heads/main", ".github/workflows/release.yml", "refs/heads/main", true},
		{repo + "/.github/workflows/release.yml@refs/tags/v1.0@x", ".github/workflows/release.yml", "refs/tags/v1.0@x", true},
		{repo + "/.github/workflows/release.yml@4c1d9e0", ".github/workflows/release.yml", "4c1d9e0", true},
		{"https://github.com/O/R/.github/workflows/a.yaml@refs/heads/main", ".github/workflows/a.yaml", "refs/heads/main", true},
		{"https://github.com/o/other/.github/workflows/a.yml@main", "", "", false},
		{repo + "/workflows/a.yml@main", "", "", false},
		{repo + "/.github/workflows/sub/a.yml@main", "", "", false},
		{repo, "", "", false},
		{"", "", "", false},
	} {
		path, ref, ok := workflowOf(c.uri, repo)
		if path != c.path || ref != c.ref || ok != c.ok {
			t.Errorf("workflowOf(%q) = %q, %q, %v", c.uri, path, ref, ok)
		}
	}
}
