package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pklaschka/lieferling/internal/attest"
	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/state"
	"github.com/pklaschka/lieferling/internal/verify"
)

// fakeAttester reads a bundle as `{"repo":"o/r","workflow":"release.yml"}`
// and verifies it when the digest is in good.
type fakeAttester struct {
	good  map[string]bool
	err   error
	calls int
}

func (f *fakeAttester) Verify(_ context.Context, bundles [][]byte, want attest.Expect) (verify.Origin, error) {
	f.calls++
	if f.err != nil {
		return verify.Origin{}, f.err
	}
	if !f.good[want.Digest] {
		return verify.Origin{}, attest.ErrInvalid
	}
	o := verify.Origin{Repository: want.Repo, Workflow: ".github/workflows/release.yml", Ref: "refs/heads/main", Commit: "4c1d9e0123456789"}
	if bytes.Contains(bundles[0], []byte("build.yml")) {
		o.Workflow = ".github/workflows/build.yml"
	}
	if want.Workflow != "" && want.Workflow != o.Workflow {
		return verify.Origin{}, &attest.Mismatch{Kind: attest.ErrWorkflow, Want: want.Workflow, Got: o.Workflow}
	}
	return o, nil
}

// attested is a store whose GitHub holds one attestation for asset1's
// default contents and whose verifier accepts it.
func attested(t *testing.T, bundle string, assets ...forge.Asset) (*Store, *fakeInstaller, *attestRoute, *fakeAttester) {
	t.Helper()
	if len(assets) == 0 {
		assets = []forge.Asset{{Name: asset1, Digest: digestOf("contents of " + asset1)}}
	}
	d := digestOf("contents of " + asset1)
	route := &attestRoute{bundles: map[string][]string{d: {bundle}}}
	s, fi := newTestStoreAttested(t, demoRelease("v1.0.0", assets...), nil, route)
	fa := &fakeAttester{good: map[string]bool{d: true}}
	s.Attest = fa
	fi.identity = senderA
	return s, fi, route, fa
}

func checkOf(rep *verify.Report, id string) verify.Check {
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	return verify.Check{}
}

func TestAttestationPasses(t *testing.T) {
	s, _, _, _ := attested(t, `{"workflow":"release.yml"}`)
	st, err := s.Install(context.Background(), "me/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := checkOf(st.Customs, verify.CheckAttestation)
	if c.Result != verify.Pass || c.Code != "verified" || st.Customs.Origin == nil || st.Customs.Origin.Repository != "me/demo" {
		t.Fatalf("customs = %+v", st.Customs)
	}
	if !strings.Contains(c.Text, "built by .github/workflows/release.yml from main at commit 4c1d9e0 in me/demo") {
		t.Errorf("text = %q", c.Text)
	}
	ls, _ := state.Load(s.StatePath)
	if in, _ := ls.Get("me/demo"); in.Customs.Origin == nil {
		t.Error("origin not in state.json")
	}
}

func TestAttestationFailsHolds(t *testing.T) {
	s, fi, _, fa := attested(t, `{}`)
	fa.good = nil
	st, err := s.Install(context.Background(), "me/demo", nil)
	if !errors.Is(err, verify.ErrHeld) || fi.placed != 0 {
		t.Fatalf("err = %v, placed = %d", err, fi.placed)
	}
	if c := checkOf(st.Customs, verify.CheckAttestation); c.Result != verify.Fail || c.Code != "invalid" {
		t.Errorf("check = %+v", c)
	}
}

func TestAttestationTrustIsTolerated(t *testing.T) {
	s, fi, _, fa := attested(t, `{}`)
	fa.err = attest.ErrTrust
	st, err := s.Install(context.Background(), "me/demo", nil)
	if err != nil || fi.placed != 1 {
		t.Fatalf("err = %v, placed = %d", err, fi.placed)
	}
	if c := checkOf(st.Customs, verify.CheckAttestation); c.Code != "trust" {
		t.Errorf("check = %+v", c)
	}
}

func TestStrictNeedsAttestation(t *testing.T) {
	// checksum fine, no attestation
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("contents of " + asset1)}))
	config.Update(s.ConfigPath, func(c *config.Config) error { c.Verify = "strict"; return nil })
	st, err := s.Install(context.Background(), "me/demo", nil)
	if !errors.Is(err, verify.ErrHeld) || fi.placed != 0 {
		t.Fatalf("err = %v, placed = %d", err, fi.placed)
	}
	if c := checkOf(st.Customs, verify.CheckPolicy); c.Code != "attestation_required" {
		t.Errorf("policy line = %+v", st.Customs.Checks)
	}
	if c := checkOf(st.Customs, verify.CheckSender); c.ID != "" {
		t.Error("the package was opened although it was held")
	}

	// with the attestation, strict clears
	s2, fi2, _, _ := attested(t, `{}`)
	config.Update(s2.ConfigPath, func(c *config.Config) error { c.Verify = "strict"; return nil })
	if _, err := s2.Install(context.Background(), "me/demo", nil); err != nil || fi2.placed != 1 {
		t.Fatalf("strict with attestation: %v", err)
	}
}

func TestForgejoUnderStrict(t *testing.T) {
	r := forge.Release{TagName: "v1.0.0", Assets: []forge.Asset{{Name: asset1}}}
	s, _ := newTestStore(t, nil)
	app := config.App{Repo: "codeberg.org/me/demo", Channel: "stable"}
	cfg := config.Config{Verify: "strict", Apps: []config.App{app}}
	var rep verify.Report
	rep.Digest = digestOf("x")
	s.checkDownload(context.Background(), cfg.Apps[0], r, r.Assets[0], &rep, "")
	if c := checkOf(&rep, verify.CheckAttestation); c.Code != "unsupported" || c.Source != "Codeberg" {
		t.Errorf("attestation = %+v", c)
	}
	v, checks := verify.Decide(verify.Strict, rep.Checks)
	if v != verify.Held || checkOf(&verify.Report{Checks: checks}, verify.CheckPolicy).ID == "" {
		t.Errorf("strict Forgejo: %s %+v", v, checks)
	}
}

func TestWorkflowPinIsMandatory(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("contents of " + asset1)}))
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", Workflow: ".github/workflows/release.yml"}}
		return nil
	})
	st, err := s.Install(context.Background(), "me/demo", nil)
	if !errors.Is(err, verify.ErrHeld) || fi.placed != 0 {
		t.Fatalf("err = %v", err)
	}
	if c := checkOf(st.Customs, verify.CheckPolicy); c.Code != "workflow_required" {
		t.Errorf("checks = %+v", st.Customs.Checks)
	}

	// another workflow built it
	s2, fi2, _, _ := attested(t, `{"workflow":"build.yml"}`)
	config.Update(s2.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", Workflow: ".github/workflows/release.yml"}}
		return nil
	})
	st, err = s2.Install(context.Background(), "me/demo", nil)
	if !errors.Is(err, verify.ErrHeld) || fi2.placed != 0 {
		t.Fatalf("err = %v", err)
	}
	if c := checkOf(st.Customs, verify.CheckAttestation); c.Code != "workflow" || c.Got != ".github/workflows/build.yml" {
		t.Errorf("check = %+v", c)
	}

	// the right one
	s3, _, _, _ := attested(t, `{}`)
	config.Update(s3.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", Workflow: ".github/workflows/release.yml"}}
		return nil
	})
	st, err = s3.Install(context.Background(), "me/demo", nil)
	if err != nil || checkOf(st.Customs, verify.CheckAttestation).Code != "pinned" {
		t.Fatalf("pinned: %v %+v", err, st.Customs)
	}
}

func TestNoAttestationAfterDigestFailure(t *testing.T) {
	s, _, route, fa := attested(t, `{}`, forge.Asset{Name: asset1, Digest: digestOf("something else")})
	if _, err := s.Install(context.Background(), "me/demo", nil); !errors.Is(err, verify.ErrHeld) {
		t.Fatal(err)
	}
	if route.calls != 0 || fa.calls != 0 {
		t.Errorf("attestations asked for after a digest failure: %d, %d", route.calls, fa.calls)
	}
}

func TestAcceptDigest(t *testing.T) {
	downloads := map[string]string{}
	s, fi := newTestStoreWith(t, demoRelease("v1.0.0", forge.Asset{Name: asset1}), downloads)
	ctx := context.Background()
	other := digestOf("what the other Mac saw")
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", Digests: map[string]string{"1.0.0/" + asset1: other}}}
		return nil
	})
	if _, err := s.Install(ctx, "me/demo", nil); !errors.Is(err, verify.ErrHeld) {
		t.Fatalf("not held: %v", err)
	}
	// another digest than the download's changes nothing
	if _, err := s.InstallAccepting(ctx, "me/demo", Accept{Digest: digestOf("nonsense")}, nil); !errors.Is(err, verify.ErrHeld) || fi.placed != 0 {
		t.Fatalf("wrong digest accepted: %v", err)
	}
	mine := digestOf("contents of " + asset1)
	st, err := s.InstallAccepting(ctx, "me/demo", Accept{Digest: strings.ToUpper(mine[:7]) + mine[7:]}, nil)
	if err != nil || fi.placed != 1 {
		t.Fatalf("accept: %v", err)
	}
	if c := checkOf(st.Customs, verify.CheckSynced); c.Result != verify.Pass || c.Code != "accepted" {
		t.Errorf("synced = %+v", c)
	}
	cfg, _ := config.Load(s.ConfigPath)
	if got := cfg.Find("me/demo").Digests["1.0.0/"+asset1]; got != mine {
		t.Errorf("entry = %q, want only %q", got, mine)
	}
}

func TestAcceptDigestCannotOverrideTheForge(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("something else")}))
	if _, err := s.InstallAccepting(context.Background(), "me/demo", Accept{Digest: digestOf("contents of " + asset1)}, nil); !errors.Is(err, verify.ErrHeld) || fi.placed != 0 {
		t.Fatalf("forge mismatch overridden: %v", err)
	}
}

func TestVerifyWritesNothing(t *testing.T) {
	releases := demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("contents of " + asset1)})
	s, fi := newTestStore(t, releases)
	ctx := context.Background()
	fi.identity = senderA
	if _, err := s.Install(ctx, "me/demo", nil); err != nil {
		t.Fatal(err)
	}
	cfgBefore, _ := os.ReadFile(s.ConfigPath)
	stBefore, _ := os.ReadFile(s.StatePath)
	placed := fi.placed

	rep, err := s.Verify(ctx, "me/demo", VerifyOptions{}, nil)
	if err != nil || rep.Verdict != verify.Cleared || rep.Summary == "" {
		t.Fatalf("verify: %v %+v", err, rep)
	}
	fi.identity = senderB
	if _, err := s.Verify(ctx, "me/demo", VerifyOptions{}, nil); !errors.Is(err, verify.ErrAsk) {
		t.Errorf("sender change: %v", err)
	}
	if rep, err := s.Verify(ctx, "me/demo", VerifyOptions{Strict: true}, nil); !errors.Is(err, verify.ErrHeld) || rep.Policy != verify.Strict {
		t.Errorf("strict: %v %s", err, rep.Policy)
	}
	cfgAfter, _ := os.ReadFile(s.ConfigPath)
	stAfter, _ := os.ReadFile(s.StatePath)
	if !bytes.Equal(cfgBefore, cfgAfter) || !bytes.Equal(stBefore, stAfter) || fi.placed != placed {
		t.Errorf("verify wrote or placed something")
	}

	config.Update(s.ConfigPath, func(c *config.Config) error { c.Apps[0].Verify = "off"; return nil })
	if rep, err := s.Verify(ctx, "me/demo", VerifyOptions{}, nil); err != nil || rep.Verdict != verify.Exempt {
		t.Errorf("off: %v %s", err, rep.Verdict)
	}
}

func TestSetPolicy(t *testing.T) {
	s, _ := newTestStore(t, nil)
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo"}, {Repo: "codeberg.org/me/werk"}}
		return nil
	})
	for _, bad := range [][2]string{{"", "off"}, {"", "default"}, {"", "paranoid"}, {"me/demo", "paranoid"}, {"other/app", "strict"}} {
		if err := s.SetPolicy(bad[0], bad[1]); err == nil {
			t.Errorf("SetPolicy(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if err := s.SetPolicy("", "Strict"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPolicy("me/demo", "off"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Policies()
	demo, werk := policyOf(p, "me/demo"), policyOf(p, "codeberg.org/me/werk")
	if p.Global != "strict" || demo.Policy != "off" || !demo.Exception || werk.Policy != "strict" || werk.Unmet == "" || demo.Unmet != "" {
		t.Errorf("policies = %+v", p)
	}
	s.SetPolicy("me/demo", "default")
	cfg, _ := config.Load(s.ConfigPath)
	if cfg.Find("me/demo").Verify != "" {
		t.Error("default did not clear the exception")
	}
}

func policyOf(p Policies, repo string) AppPolicy {
	for _, a := range p.Apps {
		if a.Repo == repo {
			return a
		}
	}
	return AppPolicy{}
}

func TestSetWorkflow(t *testing.T) {
	s, _ := newTestStore(t, nil)
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo"}, {Repo: "codeberg.org/me/werk"}}
		return nil
	})
	if err := s.SetWorkflow("me/demo", "release.yml"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkflow("codeberg.org/me/werk", "release.yml"); err == nil {
		t.Error("pin off GitHub accepted")
	}
	if err := s.SetWorkflow("me/demo", "nope"); err == nil {
		t.Error("bad pin accepted")
	}
	p, _ := s.Policies()
	if w := policyOf(p, "me/demo").Workflow; w != ".github/workflows/release.yml" {
		t.Errorf("pin = %q", w)
	}
	s.SetWorkflow("me/demo", "none")
	if p, _ := s.Policies(); policyOf(p, "me/demo").Workflow != "" {
		t.Error("none did not clear the pin")
	}
}
