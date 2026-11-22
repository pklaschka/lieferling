package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/install"
)

type fakeInstaller struct {
	installed map[string]string // asset name -> file contents
	foreign   map[string]string // app name -> version installed without Lieferling
	opened    []string          // paths passed to Open
	// identity is what the next package contains; installedIdentity is
	// what the copy at a path says.
	identity          install.Identity
	installedIdentity map[string]install.Identity
	placed            int
}

func (f *fakeInstaller) Install(ctx context.Context, file, asset string, inspect install.Inspect) (install.Result, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return install.Result{}, err
	}
	if inspect != nil {
		if err := inspect(ctx, install.Unpacked{Path: "/tmp/Demo.app", Name: "Demo", Identity: f.identity}); err != nil {
			return install.Result{}, err
		}
	}
	f.installed[asset] = string(data)
	f.placed++
	if f.installedIdentity == nil {
		f.installedIdentity = map[string]install.Identity{}
	}
	f.installedIdentity["/Applications/Demo.app"] = f.identity
	return install.Result{Path: "/Applications/Demo.app", AppName: "Demo"}, nil
}

func (f *fakeInstaller) Identity(_ context.Context, path string) install.Identity {
	return f.installedIdentity[path]
}

func (f *fakeInstaller) Uninstall(context.Context, string) error { return nil }

func (f *fakeInstaller) Open(_ context.Context, path string) error {
	f.opened = append(f.opened, path)
	return nil
}

func (f *fakeInstaller) InstalledVersion(name string) (string, string) {
	if v, ok := f.foreign[name]; ok {
		return v, "/Applications/" + name + ".app"
	}
	return "", ""
}

func newTestStore(t *testing.T, releases map[string][]forge.Release) (*Store, *fakeInstaller) {
	t.Helper()
	return newTestStoreWith(t, releases, nil)
}

// newTestStoreWith serves downloads[name] for an asset when set, else
// "contents of <name>". Tests may change downloads between calls.
func newTestStoreWith(t *testing.T, releases map[string][]forge.Release, downloads map[string]string) (*Store, *fakeInstaller) {
	t.Helper()
	return newTestStoreAttested(t, releases, downloads, nil)
}

// attestRoute serves /repos/<repo>/attestations/<digest>: bundles[digest] as
// inline bundles, 404 when there are none. calls counts the requests.
type attestRoute struct {
	bundles map[string][]string
	calls   int
}

func newTestStoreAttested(t *testing.T, releases map[string][]forge.Release, downloads map[string]string, att *attestRoute) (*Store, *fakeInstaller) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, d, ok := strings.Cut(r.URL.Path, "/attestations/"); ok {
			if att == nil {
				http.NotFound(w, r)
				return
			}
			att.calls++
			bs := att.bundles[d]
			if len(bs) == 0 {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(`{"attestations":[{"bundle":` + strings.Join(bs, `},{"bundle":`) + `}]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/download/") {
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "wrong accept", 400)
				return
			}
			name := strings.TrimPrefix(r.URL.Path, "/download/")
			if d, ok := downloads[name]; ok {
				w.Write([]byte(d))
				return
			}
			w.Write([]byte("contents of " + name))
			return
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/repos/"), "/releases")
		rs, ok := releases[repo]
		if !ok {
			http.NotFound(w, r)
			return
		}
		for i := range rs {
			for j := range rs[i].Assets {
				rs[i].Assets[j].URL = srv.URL + "/download/" + rs[i].Assets[j].Name
			}
		}
		json.NewEncoder(w).Encode(rs)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	fi := &fakeInstaller{installed: map[string]string{}, foreign: map[string]string{}}
	return &Store{
		ConfigPath: filepath.Join(dir, "icloud", "config.json"),
		StatePath:  filepath.Join(dir, "local", "state.json"),
		Forge:      &forge.Client{GitHub: &forge.GitHub{BaseURL: srv.URL}, Gitea: &forge.Gitea{}},
		Installer:  fi,
		GOOS:       "darwin",
		GOARCH:     "arm64",
	}, fi
}

func TestAddInstallUpdate(t *testing.T) {
	releases := map[string][]forge.Release{
		"me/demo": {
			{TagName: "v1.1.0-rc.1", Prerelease: true, Assets: []forge.Asset{{Name: "Demo-1.1.0-rc.1-arm64.dmg"}}},
			{TagName: "v1.0.0", Assets: []forge.Asset{{Name: "Demo-1.0.0-arm64.dmg"}, {Name: "Demo-1.0.0-x64.dmg"}}},
		},
	}
	s, fi := newTestStore(t, releases)
	ctx := context.Background()

	if _, err := s.Add(ctx, "https://github.com/me/demo.git", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, "me/missing", ""); err == nil {
		t.Error("added a repository that does not exist")
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if a := list[0]; a.Latest != "1.0.0" || a.Installed != "" || a.UpdateAvailable || a.Asset != "Demo-1.0.0-arm64.dmg" {
		t.Errorf("before install: %+v", a)
	}

	st, err := s.Install(ctx, "me/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Installed != "1.0.0" || fi.installed["Demo-1.0.0-arm64.dmg"] != "contents of Demo-1.0.0-arm64.dmg" {
		t.Errorf("install: %+v %v", st, fi.installed)
	}
	cfg, _ := config.Load(s.ConfigPath)
	if cfg.Find("me/demo").AppName != "Demo" {
		t.Error("app name not recorded in the shared configuration")
	}

	// switching to release candidates offers the update
	if err := s.SetChannel("me/demo", "prerelease"); err != nil {
		t.Fatal(err)
	}
	updates, err := s.Updates(ctx)
	if err != nil || len(updates) != 1 || updates[0].Latest != "1.1.0-rc.1" {
		t.Fatalf("updates: %+v %v", updates, err)
	}

	// another computer: shared config, empty local state, copy installed by hand
	other := &Store{
		ConfigPath: s.ConfigPath, StatePath: filepath.Join(t.TempDir(), "state.json"),
		Forge: s.Forge, Installer: s.Installer, GOOS: s.GOOS, GOARCH: s.GOARCH,
	}
	fi.foreign["Demo"] = "0.9.0"
	list, _ = other.List(ctx)
	if a := list[0]; a.Installed != "0.9.0" || a.ManagedByLieferling || !a.UpdateAvailable {
		t.Errorf("other computer: %+v", a)
	}

	// opening: the copy Lieferling installed, and the one found by name
	if err := s.Open(ctx, "me/demo"); err != nil {
		t.Fatal(err)
	}
	if err := other.Open(ctx, "me/demo"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/Applications/Demo.app", "/Applications/Demo.app"}; strings.Join(fi.opened, ",") != strings.Join(want, ",") {
		t.Errorf("opened %v, want %v", fi.opened, want)
	}
	delete(fi.foreign, "Demo")
	if err := other.Open(ctx, "me/demo"); err != install.ErrNotInstalled {
		t.Errorf("opening an app that is not installed: %v", err)
	}

	if err := s.Remove(ctx, "me/demo", true); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("after remove: %+v", list)
	}
}

func TestNormalizeRepo(t *testing.T) {
	for in, want := range map[string]string{
		"owner/name":                                "owner/name",
		"https://github.com/owner/name":             "owner/name",
		"https://github.com/owner/name.git":         "owner/name",
		"github.com/owner/name/releases/tag/v1.0.0": "owner/name",
		"git@github.com:owner/name.git":             "owner/name",
		"codeberg.org/owner/name":                   "codeberg.org/owner/name",
		"https://Codeberg.org/owner/name/releases":  "codeberg.org/owner/name",
		"git@codeberg.org:owner/name.git":           "codeberg.org/owner/name",
		"ssh://git@git.example.com:2222/o/n.git":    "git.example.com/o/n",
		"https://git.example.com:3000/o/n":          "git.example.com:3000/o/n",
		"https://www.github.com/owner/name":         "owner/name",
	} {
		if got, err := NormalizeRepo(in); err != nil || got != want {
			t.Errorf("NormalizeRepo(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "owner", "https://example.com", "codeberg.org/owner", "bad_host.org/o/n"} {
		if _, err := NormalizeRepo(bad); err == nil {
			t.Errorf("NormalizeRepo(%q) accepted", bad)
		}
	}
}

// TestForgejo installs from a Forgejo server: releases come from /api/v1,
// downloads from the browser link, and the token goes to that server only.
func TestForgejo(t *testing.T) {
	var srv *httptest.Server
	var tokens []string
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/version":
			w.Write([]byte(`{"version":"11.0.0+gitea-1.22.0"}`))
		case "/api/v1/repos/me/demo/releases":
			if r.URL.Query().Get("limit") == "" {
				http.Error(w, "no limit", 400)
				return
			}
			w.Write([]byte(`[{"tag_name":"v2.0.0","prerelease":false,"draft":false,
				"html_url":"https://` + r.Host + `/me/demo/releases/tag/v2.0.0",
				"published_at":"2026-11-01T10:00:00Z",
				"assets":[{"id":1,"name":"Demo-2.0.0-arm64.dmg","size":9,
					"browser_download_url":"https://` + r.Host + `/attachments/abc"}]}]`))
		case "/attachments/abc":
			w.Write([]byte("forgejo!!"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")

	s, fi := newTestStore(t, nil)
	s.Forge.Gitea = &forge.Gitea{HTTP: srv.Client(), Token: func(h string) string {
		if h != host {
			t.Errorf("token asked for %q", h)
		}
		return "secret"
	}}
	ctx := context.Background()

	st, err := s.Add(ctx, "https://"+host+"/me/demo/releases", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Repo != host+"/me/demo" || st.Name != "demo" || st.Forge != host || st.Latest != "2.0.0" {
		t.Errorf("add: %+v", st)
	}
	if _, err := s.Install(ctx, host+"/me/demo", nil); err != nil {
		t.Fatal(err)
	}
	if fi.installed["Demo-2.0.0-arm64.dmg"] != "forgejo!!" {
		t.Errorf("installed %v", fi.installed)
	}
	for _, tok := range tokens {
		if tok != "token secret" {
			t.Errorf("request without the token: %q", tok)
		}
	}

	_, err = s.Add(ctx, host+"/me/missing", "")
	if err == nil || !strings.Contains(err.Error(), forge.ErrNotFound.Error()) {
		t.Errorf("missing repository: %v", err)
	}
}

// A host that is no Forgejo or Gitea server says so instead of "not found".
func TestNotAForge(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	s, _ := newTestStore(t, nil)
	s.Forge.Gitea = &forge.Gitea{HTTP: srv.Client(), Token: func(string) string { return "" }}
	_, err := s.Add(context.Background(), strings.TrimPrefix(srv.URL, "https://")+"/me/demo", "")
	if err == nil || !strings.Contains(err.Error(), forge.ErrNoForge.Error()) {
		t.Errorf("got %v", err)
	}
}
