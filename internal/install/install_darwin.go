package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// New returns the macOS installer.
func New(o Options) Installer {
	if o.Dir == "" {
		o.Dir = "/Applications"
	}
	return &mac{o: o}
}

type mac struct{ o Options }

// Install does what the original install-macos.sh did: take the .app out of the
// DMG or ZIP, quit the running copy, replace the old one and clear the
// quarantine flag so macOS opens the unsigned app. inspect runs between
// unpacking and quitting, so a package it refuses leaves everything as it was.
func (m *mac) Install(ctx context.Context, file, assetName string, inspect Inspect) (Result, error) {
	u, err := unpack(ctx, file, assetName)
	if err != nil {
		return Result{}, err
	}
	defer u.close()
	q := Cleared
	if inspect != nil {
		id := assess(ctx, u.source, identify(ctx, u.source))
		if err := inspect(ctx, Unpacked{Path: u.source, Name: u.name, Identity: id}); err != nil {
			return Result{}, err
		}
		q = QuarantineFor(id, m.o.MarkNotarized)
	}
	return m.place(ctx, u, q)
}

// assess fills the signature and notarization fields of id. Each tool gets
// its own deadline; all three print to stderr.
func assess(ctx context.Context, app string, id Identity) Identity {
	out := func(name string, args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		b, err := exec.CommandContext(c, name, args...).CombinedOutput()
		return string(b), err
	}
	info, _ := out("codesign", "-dv", "--verbose=4", app)
	signed, adhoc, authority := parseSignature(info)
	switch {
	case adhoc:
		id.Signature = AdHoc
		return id
	case !signed:
		id.Signature = Unsigned
		return id
	}
	id.Authority = authority
	if v, err := out("codesign", "--verify", "--deep", "--strict", "--verbose=2", app); err != nil {
		id.Signature, id.Problem = Invalid, lastLine(v)
		return id
	}
	id.Signature = Valid
	if a, err := out("spctl", "--assess", "--type", "execute", "-vv", app); err == nil || a != "" {
		accepted, source, revoked, usable := parseAssessment(a)
		id.Source, id.Revoked, id.Assessed = source, revoked, usable
		id.Notarized = usable && accepted && source == "Notarized Developer ID"
		if revoked {
			id.Problem = lastLine(a)
		}
	}
	// stapler needs the command line tools; without them macOS would offer
	// to install them, so only look for the ticket's file then
	if exec.CommandContext(ctx, "xcode-select", "-p").Run() == nil {
		_, err := out("xcrun", "stapler", "validate", app)
		id.Stapled = err == nil
	} else {
		id.Stapled = exists(filepath.Join(app, "Contents", "CodeResources"))
	}
	return id
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (m *mac) Identity(ctx context.Context, path string) Identity {
	if path == "" || !exists(path) {
		return Identity{}
	}
	return identify(ctx, path)
}

// identify reads the bundle ID with plutil and the team with codesign. The
// team only counts when codesign confirms that a certificate from Apple's
// chain for that team signed the bundle: otherwise it is just a string that
// anyone can write into a signature.
func identify(ctx context.Context, app string) Identity {
	var id Identity
	out, err := exec.CommandContext(ctx, "plutil", "-extract", "CFBundleIdentifier", "raw",
		filepath.Join(app, "Contents", "Info.plist")).Output()
	if err == nil {
		id.BundleID = strings.TrimSpace(string(out))
	}
	// codesign -d prints to stderr, also when it fails
	info, _ := exec.CommandContext(ctx, "codesign", "-dv", "--verbose=2", app).CombinedOutput()
	team := parseTeamID(string(info))
	if team == "" {
		return id
	}
	req := fmt.Sprintf(`=anchor apple generic and certificate leaf[subject.OU] = "%s"`, team)
	if exec.CommandContext(ctx, "codesign", "--verify", "--strict", "-R"+req, app).Run() == nil {
		id.TeamID = team
	}
	return id
}

// unpacked is an app taken out of its download, not yet in place.
type unpacked struct {
	source string // the .app inside the mounted DMG or the unzipped folder
	name   string // bundle name without .app
	close  func() // detaches the DMG, then removes the work folder
}

// unpack takes the .app out of a DMG or ZIP into a work folder.
func unpack(ctx context.Context, file, assetName string) (u unpacked, err error) {
	work, err := os.MkdirTemp("", "lieferling-")
	if err != nil {
		return unpacked{}, err
	}
	cleanup := []func(){func() { os.RemoveAll(work) }}
	u.close = func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}
	defer func() {
		if err != nil {
			u.close()
		}
	}()

	switch strings.ToLower(filepath.Ext(assetName)) {
	case ".dmg":
		mount := filepath.Join(work, "mount")
		if err := os.Mkdir(mount, 0o755); err != nil {
			return u, err
		}
		if err := run(ctx, "hdiutil", "attach", file, "-nobrowse", "-readonly", "-quiet", "-mountpoint", mount); err != nil {
			return u, fmt.Errorf("opening the DMG: %w", err)
		}
		cleanup = append(cleanup, func() { exec.Command("hdiutil", "detach", mount, "-quiet", "-force").Run() })
		u.source, err = findApp(mount)
	case ".zip":
		out := filepath.Join(work, "unzipped")
		if err := run(ctx, "ditto", "-x", "-k", file, out); err != nil {
			return u, fmt.Errorf("unpacking the ZIP: %w", err)
		}
		u.source, err = findApp(out)
	default:
		return u, fmt.Errorf("cannot install %s: only .dmg and .zip are supported on macOS", assetName)
	}
	if err != nil {
		return u, err
	}
	u.name = strings.TrimSuffix(filepath.Base(u.source), ".app")
	return u, nil
}

// place puts an unpacked app into the applications folder: it quits the
// running copy, replaces the old one, clears, leaves or sets the quarantine
// flag as q says, and opens the app again if it was running.
func (m *mac) place(ctx context.Context, u unpacked, q Quarantine) (Result, error) {
	target := filepath.Join(m.o.Dir, u.name+".app")
	self := SamePath(target, m.o.Self)
	// An app that was running is opened again afterwards, like an updater
	// would. Lieferling replacing itself stays open; its caller restarts it.
	reopen := false
	if !self && running(u.name) {
		if err := quit(ctx, u.name); err != nil {
			return Result{}, err
		}
		reopen = true
	}
	// copy next to the old one first, so a failed copy leaves it in place
	next := target + ".lieferling-new"
	flag := "xattr -cr " + shellQuote(next) + " && "
	switch q {
	case Left:
		flag = ""
	case Marked:
		flag = "xattr -rw com.apple.quarantine " + shellQuote(quarantineValue(time.Now())) + " " + shellQuote(next) + " && "
	}
	script := fmt.Sprintf("rm -rf %[1]s && ditto %[2]s %[1]s && %[4]srm -rf %[3]s && mv %[1]s %[3]s",
		shellQuote(next), shellQuote(u.source), shellQuote(target), flag)
	if err := m.shell(ctx, script, !writable(m.o.Dir) || (exists(target) && !writable(target))); err != nil {
		return Result{}, fmt.Errorf("installing into %s: %w", m.o.Dir, err)
	}
	if reopen {
		_ = exec.Command("open", target).Run()
	}
	return Result{Path: target, AppName: u.name, Self: self, Quarantine: q}, nil
}

func (m *mac) Uninstall(ctx context.Context, path string) error {
	if path == "" || !strings.HasSuffix(path, ".app") || !exists(path) {
		return nil
	}
	if SamePath(path, m.o.Self) {
		return ErrSelf
	}
	if err := quit(ctx, strings.TrimSuffix(filepath.Base(path), ".app")); err != nil {
		return err
	}
	return m.shell(ctx, "rm -rf "+shellQuote(path), !writable(filepath.Dir(path)) || !writable(path))
}

func (m *mac) Open(ctx context.Context, path string) error {
	if !strings.HasSuffix(path, ".app") || !exists(path) {
		return ErrNotInstalled
	}
	return run(ctx, "open", path)
}

func (m *mac) InstalledVersion(name string) (string, string) {
	if name == "" {
		return "", ""
	}
	path := filepath.Join(m.o.Dir, name+".app")
	out, err := exec.Command("plutil", "-extract", "CFBundleShortVersionString", "raw",
		filepath.Join(path, "Contents", "Info.plist")).Output()
	if err != nil {
		return "", ""
	}
	return strings.TrimSpace(string(out)), path
}

// shell runs script, as administrator when admin is set: with sudo in a
// terminal, else through the macOS password dialog.
func (m *mac) shell(ctx context.Context, script string, admin bool) error {
	return shell(ctx, script, admin, m.o.Interactive, "")
}

// shell runs script, as administrator when admin is set: with sudo when
// interactive, else through the macOS password dialog, which shows prompt
// when given.
func shell(ctx context.Context, script string, admin, interactive bool, prompt string) error {
	switch {
	case !admin:
		return run(ctx, "/bin/sh", "-c", script)
	case interactive:
		cmd := exec.CommandContext(ctx, "sudo", "/bin/sh", "-c", script)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	default:
		esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace
		as := `do shell script "` + esc(script) + `" with administrator privileges`
		if prompt != "" {
			as += ` with prompt "` + esc(prompt) + `"`
		}
		// osascript asks from the background, so macOS can put the password
		// dialog behind Lieferling's window, where it looks as if nothing
		// happens: bring it to the front first.
		err := run(ctx, "osascript", "-e", "activate", "-e", as)
		if err != nil && strings.Contains(err.Error(), "(-128)") {
			return ErrCanceled
		}
		return err
	}
}

// LinkCommand puts a symbolic link to the command line in this app bundle
// (bundled, see BundledCommand) at path, replacing whatever is there unless
// it is a folder. It asks for an administrator password when the folder is
// not writable, as /usr/local/bin usually is not.
func LinkCommand(ctx context.Context, bundled, path string, interactive bool) error {
	if bundled == "" {
		return ErrNoCommand
	}
	if st, err := os.Lstat(path); err == nil && st.IsDir() {
		return fmt.Errorf("%s is a folder", path)
	}
	dir := filepath.Dir(path)
	script := fmt.Sprintf("mkdir -p %[1]s && rm -f %[2]s && ln -s %[3]s %[2]s",
		shellQuote(dir), shellQuote(path), shellQuote(bundled))
	prompt := fmt.Sprintf("Lieferling möchte den Befehl lieferling in %s anlegen.", dir)
	return shell(ctx, script, !creatable(dir), interactive, prompt)
}

// UnlinkCommand removes the link at path, but only when it is a symbolic
// link: a copy someone put there by hand stays.
func UnlinkCommand(ctx context.Context, path string, interactive bool) error {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s is not a link Lieferling made: remove it yourself", path)
	}
	prompt := fmt.Sprintf("Lieferling möchte den Befehl lieferling aus %s entfernen.", filepath.Dir(path))
	return shell(ctx, "rm -f "+shellQuote(path), !writable(filepath.Dir(path)), interactive, prompt)
}

// creatable reports whether dir can be made or written to without an
// administrator: the nearest folder that exists decides.
func creatable(dir string) bool {
	for {
		if _, err := os.Stat(dir); err == nil {
			return writable(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// quit asks a running app to quit (it may ask about unsaved work) and waits
// up to a minute.
func quit(ctx context.Context, name string) error {
	if !running(name) {
		return nil
	}
	_ = exec.CommandContext(ctx, "osascript", "-e", fmt.Sprintf("tell application %q to quit", name)).Run()
	for i := 0; i < 60; i++ {
		if !running(name) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%s is still running: quit it and try again", name)
}

func running(name string) bool {
	return exec.Command("pgrep", "-xq", name).Run() == nil
}

// findApp finds the .app at the top of dir or one folder down.
func findApp(dir string) (string, error) {
	for _, pattern := range []string{"*.app", "*/*.app"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", errors.New("the download holds no .app")
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func writable(p string) bool {
	f, err := os.CreateTemp(p, ".lieferling-write-test-*")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isNotDir(err) {
			return true // nothing there yet to be refused
		}
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func isNotDir(err error) bool {
	return strings.Contains(err.Error(), "not a directory")
}
