package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateMergesICloudConflictCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := Update(path, func(c *Config) error {
		c.Apps = append(c.Apps, App{Repo: "a/one", Channel: "stable"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// another Mac added an app at the same time; iCloud kept both files
	conflict := filepath.Join(dir, "config 2.json")
	if err := os.WriteFile(conflict, []byte(`{"version":1,"apps":[{"repo":"b/two","channel":"prerelease"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Apps) != 2 || cfg.Find("B/Two") == nil {
		t.Fatalf("conflict copy not merged: %+v", cfg.Apps)
	}

	if err := Update(path, func(c *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(conflict); !os.IsNotExist(err) {
		t.Error("conflict copy still there after an update")
	}
	cfg, _ = Load(path)
	if len(cfg.Apps) != 2 || cfg.Apps[0].Repo != "a/one" {
		t.Errorf("after update: %+v", cfg.Apps)
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || len(cfg.Apps) != 0 || cfg.Interval() != 6 {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}

func writeConflict(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config 2.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	digA = "sha256:aaaa"
	digB = "sha256:bbbb"
)

func TestMergeDigests(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Update(path, func(c *Config) error {
		c.Apps = []App{{Repo: "a/one", Digests: map[string]string{"1.0/a.dmg": digA, "0.9/a.dmg": digA}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeConflict(t, dir, `{"version":1,"apps":[{"repo":"a/one","digests":{"1.0/a.dmg":"`+digB+`","0.9/a.dmg":"`+digA+`","1.1/a.dmg":"`+digB+`"}}]}`)
	if err := Update(path, func(*Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load(path)
	d := cfg.Find("a/one").Digests
	if d["1.0/a.dmg"] != digA+" "+digB || d["0.9/a.dmg"] != digA || d["1.1/a.dmg"] != digB {
		t.Errorf("digests = %v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "config 2.json")); !os.IsNotExist(err) {
		t.Error("conflict copy still there")
	}
}

func TestMergeSender(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Update(path, func(c *Config) error {
		c.Apps = []App{{Repo: "a/one"}, {Repo: "b/two", BundleID: "b.two", TeamID: "TEAMAAAAAA"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeConflict(t, dir, `{"version":1,"apps":[{"repo":"a/one","bundleId":"a.one","teamId":"TEAMBBBBBB"},{"repo":"b/two","bundleId":"b.other"}]}`)
	cfg, _ := Load(path)
	if a := cfg.Find("a/one"); a.BundleID != "a.one" || a.TeamID != "TEAMBBBBBB" {
		t.Errorf("empty pair not taken: %+v", a)
	}
	if b := cfg.Find("b/two"); b.BundleID != "b.two" || b.TeamID != "TEAMAAAAAA" {
		t.Errorf("main pair not kept: %+v", b)
	}
}

func TestMergePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Update(path, func(c *Config) error {
		c.Verify = "standard"
		c.Apps = []App{{Repo: "a/one", Verify: "off"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeConflict(t, dir, `{"version":1,"verify":"strict","apps":[{"repo":"a/one","verify":"standard"}]}`)
	cfg, _ := Load(path)
	if cfg.Verify != "strict" || cfg.Find("a/one").Verify != "standard" {
		t.Errorf("policy = %q, app %q", cfg.Verify, cfg.Find("a/one").Verify)
	}
}

func TestMergeWorkflow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Update(path, func(c *Config) error {
		c.Apps = []App{{Repo: "a/one"}, {Repo: "b/two", Workflow: ".github/workflows/release.yml"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeConflict(t, dir, `{"version":1,"apps":[{"repo":"a/one","workflow":".github/workflows/build.yml"},{"repo":"b/two","workflow":".github/workflows/other.yml"}]}`)
	if err := Update(path, func(*Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load(path)
	if a := cfg.Find("a/one"); a.Workflow != ".github/workflows/build.yml" {
		t.Errorf("empty pin not filled: %+v", a)
	}
	if b := cfg.Find("b/two"); b.Workflow != ".github/workflows/release.yml" {
		t.Errorf("main pin not kept: %+v", b)
	}
}
