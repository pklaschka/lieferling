package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/pklaschka/lieferling/internal/store"
	"github.com/pklaschka/lieferling/internal/verify"
)

// verifyApp runs the customs check on the newest package of repo and
// installs nothing. The error wraps verify.ErrHeld or verify.ErrAsk when the
// package would be stopped, which exitCode turns into 3.
func verifyApp(ctx context.Context, s *store.Store, repo string, strict, asJSON bool, out io.Writer) error {
	var p store.Progress
	if !asJSON {
		p = progressLine(out, repo)
	}
	rep, err := s.Verify(ctx, repo, store.VerifyOptions{Strict: strict}, p)
	if p != nil {
		endProgress(out)
	}
	if err != nil && !isHeld(err) {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if jerr := enc.Encode(rep); jerr != nil {
			return jerr
		}
		if err != nil {
			return fmt.Errorf("%s %s would not be installed: %w", repo, rep.Version, unwrapHeld(err))
		}
		return nil
	}
	printCustoms(out, &rep)
	if rep.Digest != "" {
		fmt.Fprintf(out, "Package: %s %s, %s\n", rep.Asset, rep.Version, rep.Digest)
	}
	switch {
	case errors.Is(err, verify.ErrAsk):
		fmt.Fprintln(out, "Would ask before installing: the sender changed.")
	case err != nil:
		fmt.Fprintf(out, "Would not install %s %s.\n", repo, rep.Version)
	default:
		fmt.Fprintf(out, "Would install %s %s.\n", repo, rep.Version)
	}
	return err
}

func unwrapHeld(err error) error {
	if errors.Is(err, verify.ErrAsk) {
		return verify.ErrAsk
	}
	return verify.ErrHeld
}

// policy shows or sets the customs policy.
//
//	policy                                  show
//	policy standard|strict                  for all apps
//	policy <repo> [<policy>] [--workflow …] for one app
func policy(s *store.Store, pos []string, workflow string, workflowSet bool, out io.Writer) error {
	switch {
	case len(pos) == 0 && !workflowSet:
		return showPolicies(s, out)
	case len(pos) == 1 && !strings.Contains(pos[0], "/") && !workflowSet:
		if err := s.SetPolicy("", pos[0]); err != nil {
			return err
		}
		fmt.Fprintf(out, "The customs policy for all apps is now %s.\n", strings.ToLower(pos[0]))
		return warnUnmet(s, out)
	case len(pos) == 1 && workflowSet, len(pos) == 2:
	default:
		return usage()
	}
	repo := pos[0]
	if len(pos) == 2 {
		if err := s.SetPolicy(repo, pos[1]); err != nil {
			return err
		}
		if p := strings.ToLower(pos[1]); p == "default" {
			fmt.Fprintf(out, "%s follows the policy for all apps again.\n", repo)
		} else {
			fmt.Fprintf(out, "%s is now checked by the %s policy.\n", repo, p)
		}
	}
	if workflowSet {
		if err := s.SetWorkflow(repo, workflow); err != nil {
			return err
		}
		if w, _ := verify.NormalizeWorkflow(workflow); w != "" {
			fmt.Fprintf(out, "%s must now be built by %s, with a build attestation.\n", repo, w)
		} else {
			fmt.Fprintf(out, "%s may be built by any workflow of its repository.\n", repo)
		}
	}
	return warnUnmet(s, out)
}

func showPolicies(s *store.Store, out io.Writer) error {
	p, err := s.Policies()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Policy for all apps: %s\n", p.Global)
	if len(p.Apps) > 0 {
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "REPO\tPOLICY\tWORKFLOW\t")
		for _, a := range p.Apps {
			pol := a.Policy
			if !a.Exception {
				pol = "(" + pol + ")"
			}
			wf := a.Workflow
			if wf == "" {
				wf = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t\n", a.Repo, pol, wf)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return warnUnmet(s, out)
}

// warnUnmet names the apps that the strict policy will always hold.
func warnUnmet(s *store.Store, out io.Writer) error {
	p, err := s.Policies()
	if err != nil {
		return err
	}
	for _, a := range p.Apps {
		if a.Unmet != "" {
			fmt.Fprintf(out, "%s is %s: strict will hold it. Exempt it with: lieferling policy %s standard\n", a.Repo, a.Unmet, a.Repo)
		}
	}
	return nil
}
