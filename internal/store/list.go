package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/pick"
	"github.com/pklaschka/lieferling/internal/state"
	"github.com/pklaschka/lieferling/internal/verify"
)

// Status is what the list shows for one app.
type Status struct {
	Repo string `json:"repo"`
	// Forge names where the repository lives: GitHub, Codeberg or a host.
	Forge string `json:"forge"`
	// RepoURL is the repository's page in the browser.
	RepoURL   string `json:"repoUrl"`
	Name      string `json:"name"`
	Channel   string `json:"channel"`
	Installed string `json:"installed"` // version here, "" when not installed
	Path      string `json:"path"`
	// ManagedByLieferling is false for a copy installed some other way (found
	// through the app name another computer recorded).
	ManagedByLieferling bool   `json:"managedByLieferling"`
	Latest              string `json:"latest"` // newest version in the channel
	LatestTag           string `json:"latestTag"`
	PublishedAt         string `json:"publishedAt"`
	ReleaseURL          string `json:"releaseUrl"`
	Asset               string `json:"asset"` // file that would be installed
	UpdateAvailable     bool   `json:"updateAvailable"`
	Error               string `json:"error"`
	// Self marks the copy of Lieferling that is running; the app sets it.
	Self bool `json:"self"`
	// Customs is the customs note to show: the report of a held package of
	// the newest version, else the report of the installed copy, else nil.
	Customs *verify.Report `json:"customs"`
	// Policy is the app's own customs exception: "", "standard", "strict"
	// or "off".
	Policy string `json:"policy"`
}

// List returns the status of every app, asking the forges for all at once.
func (s *Store) List(ctx context.Context) ([]Status, error) {
	cfg, err := config.Load(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(s.StatePath)
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(cfg.Apps))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, a := range cfg.Apps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = s.status(ctx, a, st)
		}()
	}
	wg.Wait()
	return out, nil
}

func (s *Store) status(ctx context.Context, a config.App, st state.State) Status {
	out := s.statusOf(ctx, a, st)
	out.Customs = customs(a, st, out)
	out.Policy = a.Verify
	return out
}

func (s *Store) statusOf(ctx context.Context, a config.App, st state.State) Status {
	out := Status{Repo: a.Repo, Name: a.AppName, Channel: a.Channel}
	if out.Channel == "" {
		out.Channel = pick.Stable
	}
	if r, err := forge.ParseRepo(a.Repo); err == nil {
		out.Forge, out.RepoURL = r.Forge(), r.WebURL()
		if out.Name == "" {
			out.Name = r.Name
		}
	} else if out.Name == "" {
		out.Name = a.Repo
	}
	if in, ok := st.Get(a.Repo); ok {
		out.Installed, out.Path, out.ManagedByLieferling = in.Version, in.Path, true
	} else if v, p := s.Installer.InstalledVersion(a.AppName); v != "" {
		out.Installed, out.Path = v, p
	}

	releases, err := s.Forge.Releases(ctx, a.Repo)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	rel, ok := pick.Release(releases, out.Channel)
	if !ok {
		out.Error = "no release in the " + out.Channel + " channel"
		return out
	}
	out.Latest, out.LatestTag, out.ReleaseURL = pick.Version(rel.TagName), rel.TagName, rel.HTMLURL
	if !rel.PublishedAt.IsZero() {
		out.PublishedAt = rel.PublishedAt.Format(time.RFC3339)
	}
	asset, ok := pick.Asset(rel.Assets, s.GOOS, s.GOARCH)
	if !ok {
		out.Error = fmt.Sprintf("release %s has nothing to install on %s/%s", rel.TagName, s.GOOS, s.GOARCH)
		return out
	}
	out.Asset = asset.Name
	out.UpdateAvailable = out.Installed != "" && pick.Newer(out.Latest, out.Installed)
	return out
}

// Updates returns the apps with a newer release than the installed one.
func (s *Store) Updates(ctx context.Context) ([]Status, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Status
	for _, a := range all {
		if a.UpdateAvailable {
			out = append(out, a)
		}
	}
	return out, nil
}
