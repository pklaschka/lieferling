// Lieferling installs and updates apps from GitHub, Forgejo and Gitea releases. This is the app
// with a window and a menu bar icon; cmd/lieferling is the command line. Both
// share the core in internal/.
package main

import (
	"context"
	"embed"
	"log"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/pklaschka/lieferling/internal/install"
	"github.com/pklaschka/lieferling/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

// trayIcon is the menu bar symbol: a parcel with its label, drawn in black
// for macOS to tint (a template image).
//
//go:embed build/darwin/tray-template.png
var trayIcon []byte

func main() {
	svc := &AppService{store: store.New(), self: install.SelfBundle()}
	if svc.self != "" {
		opts := install.DefaultOptions()
		opts.Self = svc.self
		svc.store.Installer = install.New(opts)
	}
	notifier := notifications.New()

	app := application.New(application.Options{
		Name:        "Lieferling",
		Description: "Installs and updates apps from GitHub, Forgejo and Gitea releases",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// keep running in the menu bar when the window is closed
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	svc.app = app

	// The default menu's Help > Learn More loads wails.io into the window;
	// build the menu ourselves with a help item that opens the README.
	// The app menu is the standard one plus the command line item.
	appMenu := app.NewMenu()
	var commandItem *application.MenuItem
	if runtime.GOOS == "darwin" {
		m := appMenu.AddSubmenu("Lieferling")
		m.AddRole(application.About)
		m.AddSeparator()
		commandItem = m.Add("Kommandozeilen-Tool installieren …")
		// the customs policy for all apps; one app's exception is in its row menu
		zoll := m.AddSubmenu("Zollkontrolle")
		current := svc.Policy()
		svc.policyItems = map[string]*application.MenuItem{}
		for _, p := range []struct{ value, label string }{{"standard", "Standard"}, {"strict", "Streng"}} {
			item := zoll.AddRadio(p.label, current == p.value)
			value := p.value
			item.OnClick(func(*application.Context) {
				_ = svc.SetPolicy(context.Background(), "", value)
			})
			svc.policyItems[value] = item
		}
		zoll.AddSeparator()
		zoll.Add("Was wird geprüft?").OnClick(func(*application.Context) {
			_ = app.Browser.OpenURL("https://github.com/pklaschka/lieferling#customs-check")
		})
		m.AddSeparator()
		m.AddRole(application.ServicesMenu)
		m.AddSeparator()
		m.AddRole(application.Hide)
		m.AddRole(application.HideOthers)
		m.AddRole(application.UnHide)
		m.AddSeparator()
		m.AddRole(application.Quit)
	}
	file := appMenu.AddSubmenu("Ablage")
	catalogMenu := func(action string) func(*application.Context) {
		return func(*application.Context) {
			if w, ok := app.Window.GetByName("main"); ok {
				w.Show()
				w.Focus()
			}
			app.Event.Emit("catalog:menu", action)
		}
	}
	file.Add("Katalog importieren …").SetAccelerator("CmdOrCtrl+O").OnClick(catalogMenu("import-file"))
	file.Add("Katalog von Adresse importieren …").SetAccelerator("CmdOrCtrl+Shift+O").OnClick(catalogMenu("import-url"))
	file.AddSeparator()
	file.Add("Liste als Katalog exportieren …").SetAccelerator("CmdOrCtrl+E").OnClick(catalogMenu("export"))
	appMenu.AddRole(application.EditMenu)
	appMenu.AddRole(application.WindowMenu)
	help := appMenu.AddSubmenu("Hilfe")
	help.Add("Lieferling auf GitHub").OnClick(func(*application.Context) {
		_ = app.Browser.OpenURL("https://github.com/pklaschka/lieferling#readme")
	})
	app.Menu.SetApplicationMenu(appMenu)

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "Lieferling",
		Width:     760,
		Height:    560,
		MinWidth:  520,
		MinHeight: 360,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 52,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})
	// closing the window hides it; Lieferling keeps checking from the menu bar
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})
	show := func() {
		window.Show()
		window.Focus()
	}

	if commandItem != nil {
		// the window shows what is set up and does the work
		commandItem.OnClick(func(*application.Context) {
			show()
			app.Event.Emit("command:open")
		})
	}

	svc.relaunch = func() {
		if err := reopenAfterExit(svc.self); err == nil {
			app.Quit()
		}
	}

	// lieferling:// links, from the landing page or a click elsewhere
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		svc.openLink(e.Context().URL())
		show()
	})

	checker := &updateChecker{svc: svc, notifier: notifier}
	svc.notify = checker.notify
	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
	}
	tray.SetTooltip("Lieferling")
	menu := app.NewMenu()
	menu.Add("Lieferling öffnen").OnClick(func(*application.Context) { show() })
	menu.Add("Nach Updates suchen").OnClick(func(*application.Context) {
		go checker.check(context.Background(), true)
	})
	menu.AddSeparator()
	menu.Add("Lieferling beenden").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	checker.tray = tray

	notifier.OnNotificationResponse(func(notifications.NotificationResult) { show() })

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			if ok, err := notifier.CheckNotificationAuthorization(); err == nil && !ok {
				_, _ = notifier.RequestNotificationAuthorization()
			}
		}()
		go checker.loop()
		if svc.self != "" {
			go retireElappma(filepath.Dir(svc.self))
		}
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// updateChecker looks for updates in the background and tells the user once
// per new set of updates.
type updateChecker struct {
	svc      *AppService
	notifier *notifications.NotificationService
	tray     *application.SystemTray
	notified []string // "repo@version" already announced
}

func (c *updateChecker) loop() {
	time.Sleep(30 * time.Second) // let the Mac settle after login
	for {
		c.check(context.Background(), false)
		hours := 6
		if cfg, err := c.svc.store.Config(); err == nil {
			hours = cfg.Interval()
		}
		time.Sleep(time.Duration(hours) * time.Hour)
	}
}

// check refreshes the list. It notifies about updates not announced yet, or
// about any update (and about none) when the user asked.
func (c *updateChecker) check(ctx context.Context, asked bool) {
	list, err := c.svc.Apps(ctx)
	if err != nil {
		log.Println("update check:", err)
		return
	}
	c.svc.app.Event.Emit("apps:changed", list)

	var names, keys []string
	for _, a := range list {
		if a.UpdateAvailable {
			names = append(names, a.Name+" "+a.Latest)
			keys = append(keys, a.Repo+"@"+a.Latest)
		}
	}
	if len(keys) == 0 {
		c.tray.SetLabel("")
		if asked {
			c.notify("Alles aktuell", "Für keine App gibt es ein Update.")
		}
		return
	}
	c.tray.SetLabel("•")
	fresh := slices.ContainsFunc(keys, func(k string) bool { return !slices.Contains(c.notified, k) })
	if !fresh && !asked {
		return
	}
	c.notified = keys
	title := "Update verfügbar"
	if len(names) > 1 {
		title = "Updates verfügbar"
	}
	c.notify(title, strings.Join(names, ", "))
}

func (c *updateChecker) notify(title, body string) {
	err := c.notifier.SendNotification(notifications.NotificationOptions{
		ID:    "lieferling-updates",
		Title: title,
		Body:  body,
	})
	if err != nil {
		log.Println("notification:", err)
	}
}
