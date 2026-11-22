package main

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/pklaschka/lieferling/internal/deeplink"
	"github.com/pklaschka/lieferling/internal/icon"
	"github.com/pklaschka/lieferling/internal/install"
	"github.com/pklaschka/lieferling/internal/pick"
	"github.com/pklaschka/lieferling/internal/store"
	"github.com/pklaschka/lieferling/internal/verify"
)

// Progress is sent to the window while a download runs.
type Progress struct {
	Repo  string `json:"repo"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

func init() {
	application.RegisterEvent[Progress]("install:progress")
	application.RegisterEvent[[]store.Status]("apps:changed")
	application.RegisterEvent[application.Void]("link:opened")
	// "import-file", "import-url" or "export", from the Ablage menu
	application.RegisterEvent[string]("catalog:menu")
	application.RegisterEvent[application.Void]("command:open")
}

// AppService is what the window calls. Each method maps to one thing the
// user does; all of them leave the work to the store.
type AppService struct {
	store *store.Store
	app   *application.App

	self     string // this app's bundle, for updating itself
	relaunch func() // quits and opens the new copy
	notify   func(title, body string)

	// policyItems are the radio items of the menu Zollkontrolle, by policy
	policyItems map[string]*application.MenuItem

	mu   sync.Mutex
	last []store.Status // newest list, for the tray and notifications
	link *deeplink.Link // a deep link the window has not handled yet
}

// Apps returns the list with installed and newest versions.
func (s *AppService) Apps(ctx context.Context) ([]store.Status, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	s.remember(list)
	return list, nil
}

// Add puts a repository on the list.
func (s *AppService) Add(ctx context.Context, repo string, prerelease bool) (store.Status, error) {
	ch := pick.Stable
	if prerelease {
		ch = pick.Prerelease
	}
	st, err := s.store.Add(ctx, repo, ch)
	if err != nil {
		return st, translate(err)
	}
	s.changed(ctx)
	return st, nil
}

// Install installs or updates one app. A package held at customs is not an
// error here: the returned status carries the note in Customs.
func (s *AppService) Install(ctx context.Context, repo string) (store.Status, error) {
	st, err := s.store.Install(ctx, repo, s.progress)
	return s.installed(ctx, st, err)
}

// AcceptSender installs the package whose changed sender the user just
// confirmed.
func (s *AppService) AcceptSender(ctx context.Context, repo string) (store.Status, error) {
	st, err := s.store.AcceptSender(ctx, repo, s.progress)
	return s.installed(ctx, st, err)
}

func (s *AppService) progress(repo string, done, total int64) {
	s.app.Event.Emit("install:progress", Progress{Repo: repo, Done: done, Total: total})
}

// installed ends Install and AcceptSender.
func (s *AppService) installed(ctx context.Context, st store.Status, err error) (store.Status, error) {
	held := errors.Is(err, verify.ErrHeld) || errors.Is(err, verify.ErrAsk)
	if err != nil && !held {
		return st, translate(err)
	}
	s.present(&st)
	if held {
		if st.Self && s.notify != nil && st.Customs != nil {
			s.notify("Lieferling-Update angehalten", st.Customs.Summary)
		}
		s.changed(ctx)
		return st, nil
	}
	if install.SamePath(st.Path, s.self) && s.relaunch != nil {
		// Lieferling just replaced itself: start the new copy
		go s.relaunch()
		return st, nil
	}
	s.changed(ctx)
	return st, nil
}

// SetChannel switches between stable releases and release candidates.
func (s *AppService) SetChannel(ctx context.Context, repo string, prerelease bool) error {
	ch := pick.Stable
	if prerelease {
		ch = pick.Prerelease
	}
	if err := s.store.SetChannel(repo, ch); err != nil {
		return translate(err)
	}
	s.changed(ctx)
	return nil
}

// Remove takes an app off the list and, with uninstall, off this Mac.
func (s *AppService) Remove(ctx context.Context, repo string, uninstall bool) error {
	if err := s.store.Remove(ctx, repo, uninstall); err != nil {
		return translate(err)
	}
	s.changed(ctx)
	return nil
}

// Open starts an installed app.
func (s *AppService) Open(ctx context.Context, repo string) error {
	if err := s.store.Open(ctx, repo); err != nil {
		return translate(err)
	}
	return nil
}

// Icon returns the app's icon as a data URL, or "" when it has none.
func (s *AppService) Icon(ctx context.Context, repo string) (string, error) {
	ic, err := s.store.Icon(ctx, repo)
	if errors.Is(err, icon.ErrNone) {
		return "", nil
	}
	if err != nil {
		return "", translate(err)
	}
	return "data:" + ic.MIME + ";base64," + base64.StdEncoding.EncodeToString(ic.Data), nil
}

// SetPolicy sets the customs policy: for all apps with repo "" ("standard"
// or "strict"), or one app's exception ("standard", "strict", "off" or
// "default").
func (s *AppService) SetPolicy(ctx context.Context, repo, policy string) error {
	if err := s.store.SetPolicy(repo, policy); err != nil {
		s.showPolicy()
		return translate(err)
	}
	s.showPolicy()
	s.changed(ctx)
	return nil
}

// Policy is the customs policy for all apps: "standard" or "strict".
func (s *AppService) Policy() string {
	p, err := s.store.Policies()
	if err != nil {
		return string(verify.Standard)
	}
	return p.Global
}

// showPolicy checks the menu item of the current policy, which may have
// changed on another Mac or on the command line.
func (s *AppService) showPolicy() {
	if item, ok := s.policyItems[s.Policy()]; ok && !item.Checked() {
		item.SetChecked(true)
	}
}

// ConfigPath says where the shared list lives.
func (s *AppService) ConfigPath() string { return s.store.ConfigPath }

func (s *AppService) changed(ctx context.Context) {
	go func() {
		list, err := s.store.List(context.WithoutCancel(ctx))
		if err != nil {
			return
		}
		s.remember(list)
		s.app.Event.Emit("apps:changed", list)
	}()
}

// present prepares a status for the window: it marks the running copy of
// Lieferling and writes the customs note in German.
func (s *AppService) present(st *store.Status) {
	st.Self = s.self != "" && install.SamePath(st.Path, s.self)
	verify.Localize(st.Customs, verify.German)
}

func (s *AppService) remember(list []store.Status) {
	translateAll(list)
	for i := range list {
		s.present(&list[i])
	}
	s.mu.Lock()
	s.last = list
	s.mu.Unlock()
	s.showPolicy()
}
