package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pklaschka/lieferling/internal/forge"
	inst "github.com/pklaschka/lieferling/internal/install"
	"github.com/pklaschka/lieferling/internal/store"
	"github.com/pklaschka/lieferling/internal/verify"
)

type fakeInstaller struct {
	identity inst.Identity
	placed   map[string]inst.Identity
}

func (f *fakeInstaller) Install(ctx context.Context, _, _ string, inspect inst.Inspect) (inst.Result, error) {
	if inspect != nil {
		if err := inspect(ctx, inst.Unpacked{Name: "Demo", Identity: f.identity}); err != nil {
			return inst.Result{}, err
		}
	}
	f.placed["/Applications/Demo.app"] = f.identity
	return inst.Result{Path: "/Applications/Demo.app", AppName: "Demo"}, nil
}
func (f *fakeInstaller) Identity(_ context.Context, p string) inst.Identity { return f.placed[p] }
func (f *fakeInstaller) Uninstall(context.Context, string) error            { return nil }
func (f *fakeInstaller) InstalledVersion(string) (string, string)           { return "", "" }
func (f *fakeInstaller) Open(context.Context, string) error                 { return nil }

// customsStore serves one release of me/demo whose tag and forge digest the
// test can change.
func customsStore(t *testing.T, tag *string, digest *string) (*store.Store, *fakeInstaller) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download/") {
			fmt.Fprint(w, "bytes of "+*tag)
			return
		}
		if strings.Contains(r.URL.Path, "/attestations/") {
			http.NotFound(w, r)
			return
		}
		name := "Demo-" + strings.TrimPrefix(*tag, "v") + "-arm64.dmg"
		json.NewEncoder(w).Encode([]forge.Release{{TagName: *tag, Assets: []forge.Asset{{Name: name, URL: srv.URL + "/download/" + name, Digest: *digest}}}})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	fi := &fakeInstaller{placed: map[string]inst.Identity{}}
	return &store.Store{
		ConfigPath: filepath.Join(dir, "config.json"), StatePath: filepath.Join(dir, "state.json"),
		Forge:     &forge.Client{GitHub: &forge.GitHub{BaseURL: srv.URL}, Gitea: &forge.Gitea{}},
		Installer: fi, GOOS: "darwin", GOARCH: "arm64",
	}, fi
}

// senderChanged installs v1 from one sender and offers v2 from another.
func senderChanged(t *testing.T) (*store.Store, *fakeInstaller) {
	tag, digest := "v1.0.0", ""
	s, fi := customsStore(t, &tag, &digest)
	fi.identity = inst.Identity{BundleID: "de.example.demo", TeamID: "ABCDE12345"}
	if err := run(context.Background(), s, []string{"install", "me/demo"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	tag = "v2.0.0"
	fi.identity = inst.Identity{BundleID: "de.example.demo"}
	return s, fi
}

func setTerminal(t *testing.T, on bool, input string) {
	t.Helper()
	oldTerm, oldIn := isTerminal, stdin
	isTerminal = func() bool { return on }
	stdin = strings.NewReader(input)
	t.Cleanup(func() { isTerminal, stdin = oldTerm, oldIn })
}

func TestInstallAsksOnTerminal(t *testing.T) {
	for _, c := range []struct {
		answer    string
		installed bool
	}{{"y\n", true}, {"n\n", false}, {"\n", false}} {
		s, fi := senderChanged(t)
		setTerminal(t, true, c.answer)
		var out bytes.Buffer
		err := run(context.Background(), s, []string{"update", "me/demo"}, &out)
		if !strings.Contains(out.String(), "Install it anyway? [y/N]") || !strings.Contains(out.String(), "  FAIL  The sender changed") {
			t.Errorf("%q: output %s", c.answer, out.String())
		}
		got := fi.placed["/Applications/Demo.app"].TeamID == ""
		if got != c.installed || (err == nil) != c.installed {
			t.Errorf("%q: installed=%v err=%v", c.answer, got, err)
		}
	}
}

func TestNoPromptWithoutTerminal(t *testing.T) {
	s, _ := senderChanged(t)
	setTerminal(t, false, "y\n")
	var out bytes.Buffer
	err := run(context.Background(), s, []string{"install", "me/demo"}, &out)
	if !errors.Is(err, verify.ErrAsk) || !strings.Contains(err.Error(), "--accept-sender") || strings.Contains(out.String(), "[y/N]") {
		t.Errorf("err = %v, out = %s", err, out.String())
	}
	if exitCode(err) != 3 {
		t.Errorf("exit code %d", exitCode(err))
	}
}

func TestAcceptSenderFlag(t *testing.T) {
	s, fi := senderChanged(t)
	setTerminal(t, false, "")
	var out bytes.Buffer
	if err := run(context.Background(), s, []string{"install", "me/demo", "--accept-sender"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if fi.placed["/Applications/Demo.app"].TeamID != "" || !strings.Contains(out.String(), "Accepting the new sender de.example.demo (no team ID)") {
		t.Errorf("out = %s", out.String())
	}
}

func TestHeldExitsWithThree(t *testing.T) {
	tag, digest := "v1.0.0", "sha256:"+strings.Repeat("0", 64)
	s, fi := customsStore(t, &tag, &digest)
	if _, err := s.Add(context.Background(), "me/demo", ""); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run(context.Background(), s, []string{"install", "me/demo", "--accept-sender"}, &out)
	if !errors.Is(err, verify.ErrHeld) || exitCode(err) != 3 || len(fi.placed) != 0 {
		t.Fatalf("err = %v placed=%v", err, fi.placed)
	}
	if !strings.Contains(out.String(), "Customs: 1.0.0 held and not installed") {
		t.Errorf("out = %s", out.String())
	}
	err = run(context.Background(), s, []string{"update", "me/demo"}, &bytes.Buffer{})
	if exitCode(err) != 3 {
		t.Errorf("update exit code %d (%v)", exitCode(err), err)
	}
	out.Reset()
	if err := run(context.Background(), s, []string{"list"}, &out); err != nil || !strings.Contains(out.String(), "(held at customs)") {
		t.Errorf("list: %v %s", err, out.String())
	}
	if exitCode(errUsage) != 2 || exitCode(errors.New("x")) != 1 {
		t.Error("other exit codes changed")
	}
}
