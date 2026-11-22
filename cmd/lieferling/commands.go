package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/pklaschka/lieferling/internal/store"
	"github.com/pklaschka/lieferling/internal/verify"
)

// listApps prints the apps on the list with their installed and newest version.
func listApps(ctx context.Context, s *store.Store, out io.Writer) error {
	list, err := s.List(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "No apps yet. Add one with: lieferling add owner/name (or host/owner/name)")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tCHANNEL\tINSTALLED\tNEWEST\t")
	for _, a := range list {
		installed := a.Installed
		if installed == "" {
			installed = "-"
		}
		newest := a.Latest
		switch {
		case a.Error != "":
			newest = "error: " + a.Error
		case a.Customs != nil && (a.Customs.Verdict == verify.Held || a.Customs.Verdict == verify.Ask):
			newest += "  (held at customs)"
		case a.UpdateAvailable:
			newest += "  (update)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", a.Repo, a.Channel, installed, newest)
	}
	return w.Flush()
}

// updateApps installs the newest release of the given apps, or of every app
// with an update; it goes on after a failure and names all that failed.
func updateApps(ctx context.Context, s *store.Store, repos []string, acc store.Accept, acceptSender bool, out io.Writer) error {
	if len(repos) == 0 {
		updates, err := s.Updates(ctx)
		if err != nil {
			return err
		}
		if len(updates) == 0 {
			fmt.Fprintln(out, "Everything is up to date.")
			return nil
		}
		for _, u := range updates {
			repos = append(repos, u.Repo)
		}
	}
	var failed []string
	allHeld := true
	for _, r := range repos {
		if err := install(ctx, s, r, acc, acceptSender, out); err != nil {
			fmt.Fprintf(os.Stderr, "lieferling: %s: %v\n", r, err)
			failed = append(failed, r)
			allHeld = allHeld && isHeld(err)
		}
	}
	if len(failed) > 0 {
		if allHeld {
			return fmt.Errorf("could not update %s: %w", strings.Join(failed, ", "), verify.ErrHeld)
		}
		return fmt.Errorf("could not update %s", strings.Join(failed, ", "))
	}
	return nil
}

// isHeld reports whether err only says that customs stopped a package.
func isHeld(err error) bool {
	return errors.Is(err, verify.ErrHeld) || errors.Is(err, verify.ErrAsk)
}

// isTerminal is a variable so tests can answer the prompt (through stdin,
// see catalog.go).
var isTerminal = func() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// install installs or updates one app, showing the download's progress and
// the customs report. When only the sender changed it asks on a terminal, or
// accepts at once with acceptSender. acc.Digest stays accepted for the second
// call.
func install(ctx context.Context, s *store.Store, repo string, acc store.Accept, acceptSender bool, out io.Writer) error {
	st, err := withProgress(out, repo, func(p store.Progress) (store.Status, error) { return s.InstallAccepting(ctx, repo, acc, p) })
	printCustoms(out, st.Customs)
	if errors.Is(err, verify.ErrAsk) {
		switch {
		case acceptSender:
		case isTerminal():
			fmt.Fprint(out, "Install it anyway? [y/N] ")
			line, _ := bufio.NewReader(stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				return err
			}
		default:
			return fmt.Errorf("%w (run again with --accept-sender to accept the new sender)", err)
		}
		if st.Customs != nil && st.Customs.After != nil {
			fmt.Fprintf(out, "Accepting the new sender %s.\n", st.Customs.After)
		}
		again := store.Accept{Sender: true, Digest: acc.Digest}
		st, err = withProgress(out, repo, func(p store.Progress) (store.Status, error) { return s.InstallAccepting(ctx, repo, again, p) })
		printCustoms(out, st.Customs)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Installed %s %s at %s.\n", st.Name, st.Installed, st.Path)
	return nil
}

func withProgress(out io.Writer, repo string, fn func(store.Progress) (store.Status, error)) (store.Status, error) {
	st, err := fn(progressLine(out, repo))
	endProgress(out)
	return st, err
}

// shown is whether a progress line is open and needs a line break.
var shown bool

// progressLine prints the download's progress in steps of 5 %.
func progressLine(out io.Writer, repo string) store.Progress {
	last := -1
	return func(_ string, done, total int64) {
		if total <= 0 {
			return
		}
		if pct := int(done * 100 / total); pct/5 != last/5 {
			last, shown = pct, true
			fmt.Fprintf(out, "\rDownloading %s... %3d%%", repo, pct)
		}
	}
}

func endProgress(out io.Writer) {
	if shown {
		fmt.Fprintln(out)
		shown = false
	}
}

// printCustoms prints the report: the summary and one line per check.
func printCustoms(out io.Writer, r *verify.Report) {
	if r == nil {
		return
	}
	fmt.Fprintf(out, "Customs: %s\n", r.Summary)
	for _, c := range r.Checks {
		mark := "  --    "
		switch c.Result {
		case verify.Pass:
			mark = "  ok    "
		case verify.Fail:
			mark = "  FAIL  "
		}
		fmt.Fprintln(out, mark+c.Text)
	}
}
