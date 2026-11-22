package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/install"
	"github.com/pklaschka/lieferling/internal/state"
	"github.com/pklaschka/lieferling/internal/verify"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func digestOf(s string) string { return "sha256:" + sum(s) }

const asset1 = "Demo-1.0.0-arm64.dmg"

func demoRelease(tag string, assets ...forge.Asset) map[string][]forge.Release {
	return map[string][]forge.Release{"me/demo": {{TagName: tag, Assets: assets}}}
}

var (
	senderA = install.Identity{BundleID: "de.example.demo", TeamID: "ABCDE12345"}
	senderB = install.Identity{BundleID: "de.example.demo"}
)

func TestInstallRecordsDigestAndSender(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("contents of " + asset1)}))
	fi.identity = senderA
	st, err := s.Install(context.Background(), "me/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Customs == nil || st.Customs.Verdict != verify.Cleared {
		t.Fatalf("customs = %+v", st.Customs)
	}
	if st.Customs.Summary != "checked and cleared" || st.Customs.Checks[0].Text == "" {
		t.Errorf("not localized: %+v", st.Customs)
	}
	cfg, _ := config.Load(s.ConfigPath)
	a := cfg.Find("me/demo")
	if a.Digests["1.0.0/"+asset1] != digestOf("contents of "+asset1) || a.BundleID != senderA.BundleID || a.TeamID != senderA.TeamID {
		t.Errorf("config: %+v", a)
	}
	ls, _ := state.Load(s.StatePath)
	in, _ := ls.Get("me/demo")
	if in.Customs == nil || in.Customs.Verdict != verify.Cleared || in.Customs.Summary != "" {
		t.Errorf("state: %+v", in.Customs)
	}
}

func TestNothingToCompareIsUnchecked(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1}))
	fi.identity = senderB
	st, err := s.Install(context.Background(), "me/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Customs.Verdict != verify.Unchecked {
		t.Errorf("verdict = %s", st.Customs.Verdict)
	}
}

func TestForgeDigestMismatchHolds(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("something else")}))
	st, err := s.Install(context.Background(), "me/demo", nil)
	if !errors.Is(err, verify.ErrHeld) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "differs from the one GitHub lists") {
		t.Errorf("error does not say why: %v", err)
	}
	if fi.placed != 0 {
		t.Error("installed a held package")
	}
	if st.Customs == nil || st.Customs.Verdict != verify.Held {
		t.Fatalf("status customs = %+v", st.Customs)
	}
	cfg, _ := config.Load(s.ConfigPath)
	if a := cfg.Find("me/demo"); a != nil && len(a.Digests) > 0 {
		t.Errorf("digest recorded for a held package: %v", a.Digests)
	}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "lieferling-download-*-"+asset1))
	if len(matches) > 0 {
		t.Errorf("download left behind: %v", matches)
	}
	if _, err := s.Add(context.Background(), "me/demo", ""); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(context.Background())
	if list[0].Customs == nil || list[0].Customs.Verdict != verify.Held {
		t.Errorf("list does not show the held package: %+v", list[0].Customs)
	}
}

func TestChecksumFile(t *testing.T) {
	good := sum("contents of "+asset1) + "  " + asset1 + "\n"
	for _, c := range []struct {
		name    string
		file    string
		size    int64
		verdict verify.Verdict
		code    string
	}{
		{"matching", good, 0, verify.Cleared, "match"},
		{"mismatching", sum("other") + "  " + asset1 + "\n", 0, verify.Held, "mismatch"},
		{"unlisted", sum("other") + "  Other.dmg\n", 0, verify.Unchecked, "unlisted"},
		{"too large", good, verify.MaxChecksumFile + 1, verify.Unchecked, "unreadable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newTestStoreWith(t, demoRelease("v1.0.0", forge.Asset{Name: asset1}, forge.Asset{Name: "SHA256SUMS", Size: c.size}),
				map[string]string{"SHA256SUMS": c.file})
			st, _ := s.Install(context.Background(), "me/demo", nil)
			if st.Customs == nil || st.Customs.Verdict != c.verdict {
				t.Fatalf("customs = %+v", st.Customs)
			}
			if got := st.Customs.Checks[1]; got.ID != verify.CheckChecksums || got.Code != c.code {
				t.Errorf("checksum check = %+v", got)
			}
		})
	}
}

func TestStrictNeedsChecksum(t *testing.T) {
	s, _ := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1}))
	config.Update(s.ConfigPath, func(c *config.Config) error { c.Verify = "strict"; return nil })
	if _, err := s.Install(context.Background(), "me/demo", nil); !errors.Is(err, verify.ErrHeld) {
		t.Fatalf("err = %v", err)
	}
}

func TestPolicyOff(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("something else")}))
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", Verify: "off"}}
		return nil
	})
	st, err := s.Install(context.Background(), "me/demo", nil)
	if err != nil || fi.placed != 1 || st.Customs.Verdict != verify.Exempt {
		t.Fatalf("off: %v placed=%d %+v", err, fi.placed, st.Customs)
	}
	cfg, _ := config.Load(s.ConfigPath)
	if a := cfg.Find("me/demo"); len(a.Digests) > 0 || a.BundleID != "" {
		t.Errorf("exempt install wrote %+v", a)
	}
}

func TestSecondMacSeesAnotherPackage(t *testing.T) {
	downloads := map[string]string{}
	s, _ := newTestStoreWith(t, demoRelease("v1.0.0", forge.Asset{Name: asset1}), downloads)
	ctx := context.Background()
	if _, err := s.Install(ctx, "me/demo", nil); err != nil {
		t.Fatal(err)
	}
	other := &Store{
		ConfigPath: s.ConfigPath, StatePath: filepath.Join(t.TempDir(), "state.json"),
		Forge: s.Forge, Installer: &fakeInstaller{installed: map[string]string{}, foreign: map[string]string{}},
		GOOS: s.GOOS, GOARCH: s.GOARCH,
	}
	downloads[asset1] = "swapped bytes"
	if _, err := other.Install(ctx, "me/demo", nil); !errors.Is(err, verify.ErrHeld) {
		t.Fatalf("second Mac: %v", err)
	}
	cfg, _ := config.Load(s.ConfigPath)
	if seen := verify.Seen(cfg.Find("me/demo").Digests["1.0.0/"+asset1]); len(seen) != 2 {
		t.Errorf("entry = %v", seen)
	}
	list, _ := s.List(ctx)
	if list[0].Customs == nil || list[0].Customs.Verdict != verify.Disputed {
		t.Errorf("first Mac: %+v", list[0].Customs)
	}
	// a third try on either Mac is held as contested
	delete(downloads, asset1)
	if _, err := other.Install(ctx, "me/demo", nil); !errors.Is(err, verify.ErrHeld) {
		t.Errorf("contested version installed: %v", err)
	}
}

func TestSenderChangeAsks(t *testing.T) {
	releases := demoRelease("v1.0.0", forge.Asset{Name: asset1})
	downloads := map[string]string{}
	s, fi := newTestStoreWith(t, releases, downloads)
	ctx := context.Background()
	fi.identity = senderA
	if _, err := s.Install(ctx, "me/demo", nil); err != nil {
		t.Fatal(err)
	}
	releases["me/demo"][0] = forge.Release{TagName: "v1.1.0", Assets: []forge.Asset{{Name: "Demo-1.1.0-arm64.dmg"}}}
	fi.identity = senderB
	st, err := s.Install(ctx, "me/demo", nil)
	if !errors.Is(err, verify.ErrAsk) || fi.placed != 1 {
		t.Fatalf("err = %v, placed = %d", err, fi.placed)
	}
	if c := st.Customs; c == nil || c.Verdict != verify.Ask || c.Before == nil || c.After == nil || c.After.BundleID != senderB.BundleID {
		t.Fatalf("customs = %+v", st.Customs)
	}

	// the package changes before the user says yes: not installed
	downloads["Demo-1.1.0-arm64.dmg"] = "other bytes"
	if _, err := s.AcceptSender(ctx, "me/demo", nil); !errors.Is(err, verify.ErrAsk) || fi.placed != 1 {
		t.Fatalf("accepted a different package: %v placed=%d", err, fi.placed)
	}
	// the new report is waiting; accepting it installs
	st, err = s.AcceptSender(ctx, "me/demo", nil)
	if err != nil || fi.placed != 2 {
		t.Fatalf("accept: %v placed=%d", err, fi.placed)
	}
	if st.Customs.Verdict != verify.Cleared && st.Customs.Verdict != verify.Unchecked {
		t.Errorf("after accept: %s", st.Customs.Verdict)
	}
	cfg, _ := config.Load(s.ConfigPath)
	if a := cfg.Find("me/demo"); a.BundleID != senderB.BundleID || a.TeamID != "" {
		t.Errorf("new sender not written: %+v", a)
	}
	if _, err := s.AcceptSender(ctx, "me/demo", nil); err == nil {
		t.Error("accepted with nothing waiting")
	}
}

func TestSenderChangeHoldsUnderStrict(t *testing.T) {
	releases := demoRelease("v1.0.0", forge.Asset{Name: asset1, Digest: digestOf("contents of " + asset1)})
	next := "Demo-1.1.0-arm64.dmg"
	route := &attestRoute{bundles: map[string][]string{digestOf("contents of " + next): {`{}`}}}
	s, fi := newTestStoreAttested(t, releases, nil, route)
	s.Attest = &fakeAttester{good: map[string]bool{digestOf("contents of " + next): true}}
	ctx := context.Background()
	fi.identity = senderA
	if _, err := s.Install(ctx, "me/demo", nil); err != nil {
		t.Fatal(err)
	}
	config.Update(s.ConfigPath, func(c *config.Config) error { c.Verify = "strict"; return nil })
	releases["me/demo"][0] = forge.Release{TagName: "v1.1.0", Assets: []forge.Asset{{Name: next, Digest: digestOf("contents of " + next)}}}
	fi.identity = senderB
	st, err := s.Install(ctx, "me/demo", nil)
	if !errors.Is(err, verify.ErrHeld) {
		t.Fatalf("err = %v", err)
	}
	if st.Customs.FirstFailure().ID != verify.CheckSender {
		t.Errorf("held for another reason: %+v", st.Customs.Checks)
	}
	if _, err := s.AcceptSender(ctx, "me/demo", nil); err == nil {
		t.Error("a strict hold could be accepted")
	}
}

func TestSenderFromInstalledCopy(t *testing.T) {
	s, fi := newTestStore(t, demoRelease("v1.0.0", forge.Asset{Name: asset1}))
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", AppName: "Demo"}}
		return nil
	})
	fi.foreign["Demo"] = "0.9.0"
	fi.installedIdentity = map[string]install.Identity{"/Applications/Demo.app": senderA}
	fi.identity = senderB
	if _, err := s.Install(context.Background(), "me/demo", nil); !errors.Is(err, verify.ErrAsk) {
		t.Fatalf("err = %v", err)
	}
}

func TestPruneDigests(t *testing.T) {
	d := map[string]string{}
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0"} {
		d[v+"/a.dmg"] = "x"
	}
	d["1.5.0/b.dmg"] = "x"
	pruneDigests(d, 5, "1.5.0/a.dmg")
	if _, ok := d["1.0.0/a.dmg"]; ok || len(d) != 6 {
		t.Errorf("pruned: %v", d)
	}
	// the entry just written stays even when it is the oldest
	d["0.1.0/a.dmg"] = "x"
	pruneDigests(d, 5, "0.1.0/a.dmg")
	if _, ok := d["0.1.0/a.dmg"]; !ok {
		t.Error("dropped the current entry")
	}
}
