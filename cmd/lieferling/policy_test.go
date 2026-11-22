package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pklaschka/lieferling/internal/config"
	inst "github.com/pklaschka/lieferling/internal/install"
	"github.com/pklaschka/lieferling/internal/store"
	"github.com/pklaschka/lieferling/internal/verify"
)

func TestReorderValueFlags(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"me/demo", "--workflow", "release.yml"}, []string{"--workflow", "release.yml", "me/demo"}},
		{[]string{"--workflow", "release.yml", "me/demo", "strict"}, []string{"--workflow", "release.yml", "me/demo", "strict"}},
		{[]string{"me/demo", "--workflow=release.yml", "strict"}, []string{"--workflow=release.yml", "me/demo", "strict"}},
		{[]string{"add", "--prerelease", "x"}, []string{"--prerelease", "add", "x"}},
		{[]string{"me/demo", "--accept-digest", "sha256:ab"}, []string{"--accept-digest", "sha256:ab", "me/demo"}},
	} {
		if got := reorder(c.in); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("reorder(%q) = %q", c.in, got)
		}
	}
}

func TestPolicyCommand(t *testing.T) {
	tag, digest := "v1.0.0", ""
	s, _ := customsStore(t, &tag, &digest)
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo"}, {Repo: "codeberg.org/me/werk"}}
		return nil
	})
	ctx := context.Background()
	var out bytes.Buffer
	if err := run(ctx, s, []string{"policy", "strict"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "now strict") || !strings.Contains(out.String(), "Exempt it with: lieferling policy codeberg.org/me/werk standard") {
		t.Errorf("out = %s", out.String())
	}
	for _, args := range [][]string{
		{"policy", "me/demo", "--workflow", "release.yml"},
		{"policy", "codeberg.org/me/werk", "standard"},
		{"policy", "--workflow=release.yml", "me/demo", "off"},
	} {
		out.Reset()
		if err := run(ctx, s, args, &out); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	out.Reset()
	if err := run(ctx, s, []string{"policy"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Policy for all apps: strict", "me/demo", "off", ".github/workflows/release.yml", "standard"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Exempt it") {
		t.Errorf("warns although the exception is set:\n%s", out.String())
	}
	if err := run(ctx, s, []string{"policy", "paranoid"}, &out); err == nil {
		t.Error("unknown policy accepted")
	}
	if err := run(ctx, s, []string{"policy", "a/b", "c", "d"}, &out); !errors.Is(err, errUsage) {
		t.Errorf("three words: %v", err)
	}
	// a bad workflow changes nothing, not even the policy given with it
	if err := run(ctx, s, []string{"policy", "codeberg.org/me/werk", "strict", "--workflow", "release.yml"}, &out); err == nil {
		t.Error("pin off GitHub accepted")
	}
	if p, _ := s.Policies(); policyOf(p, "codeberg.org/me/werk").Policy != "standard" {
		t.Error("the policy was written although the workflow was refused")
	}
	for _, args := range [][]string{{"add", "x/y", "--workflow", "a.yml"}, {"verify", "me/demo", "--accept-digest", "sha256:" + strings.Repeat("a", 64)}, {"install", "me/demo", "--json"}} {
		if err := run(ctx, s, args, &out); !errors.Is(err, errUsage) {
			t.Errorf("%q: %v", args, err)
		}
	}
}

func policyOf(p store.Policies, repo string) store.AppPolicy {
	for _, a := range p.Apps {
		if a.Repo == repo {
			return a
		}
	}
	return store.AppPolicy{}
}

func TestVerifyCommand(t *testing.T) {
	tag, digest := "v1.0.0", ""
	s, fi := customsStore(t, &tag, &digest)
	fi.identity = inst.Identity{BundleID: "de.example.demo"}
	ctx := context.Background()
	var out bytes.Buffer
	if err := run(ctx, s, []string{"verify", "me/demo"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Would install me/demo 1.0.0.") || len(fi.placed) != 0 {
		t.Errorf("out = %s placed = %v", out.String(), fi.placed)
	}
	out.Reset()
	err := run(ctx, s, []string{"verify", "me/demo", "--strict"}, &out)
	if exitCode(err) != 3 || !strings.Contains(out.String(), "Would not install") {
		t.Errorf("strict: %v %s", err, out.String())
	}
	out.Reset()
	err = run(ctx, s, []string{"verify", "--json", "--strict", "me/demo"}, &out)
	var rep verify.Report
	if jerr := json.Unmarshal(out.Bytes(), &rep); jerr != nil || rep.Verdict != verify.Held || exitCode(err) != 3 {
		t.Errorf("json: %v %v %q", jerr, err, out.String())
	}
}

func TestAcceptDigestFlag(t *testing.T) {
	tag, digest := "v1.0.0", ""
	s, fi := customsStore(t, &tag, &digest)
	ctx := context.Background()
	h := sha256.Sum256([]byte("bytes of v1.0.0"))
	mine := "sha256:" + hex.EncodeToString(h[:])
	config.Update(s.ConfigPath, func(c *config.Config) error {
		c.Apps = []config.App{{Repo: "me/demo", Channel: "stable", Digests: map[string]string{"1.0.0/Demo-1.0.0-arm64.dmg": "sha256:" + strings.Repeat("1", 64)}}}
		return nil
	})
	if err := run(ctx, s, []string{"install", "me/demo"}, &bytes.Buffer{}); exitCode(err) != 3 {
		t.Fatalf("not held: %v", err)
	}
	for _, bad := range [][]string{{"install", "me/demo", "--accept-digest", "abc"}, {"update", "--accept-digest", mine}} {
		if err := run(ctx, s, bad, &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	var out bytes.Buffer
	if err := run(ctx, s, []string{"install", "me/demo", "--accept-digest", strings.ToUpper(mine)}, &out); err != nil || len(fi.placed) != 1 {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
