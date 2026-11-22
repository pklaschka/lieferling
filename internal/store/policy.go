package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/verify"
)

// SetPolicy sets the customs policy for all apps (repo "") to "standard" or
// "strict", or an app's exception to "standard", "strict", "off" or
// "default" (no exception). Unknown values are an error here, although
// verify.Resolve would read them as strict.
func (s *Store) SetPolicy(repo, policy string) error {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if repo == "" {
		if policy != string(verify.Standard) && policy != string(verify.Strict) {
			return fmt.Errorf("unknown policy %q for all apps: use standard or strict (off exists only for one app)", policy)
		}
		return config.Update(s.ConfigPath, func(c *config.Config) error {
			c.Verify = policy
			return nil
		})
	}
	switch policy {
	case string(verify.Standard), string(verify.Strict), string(verify.Off):
	case "default":
		policy = ""
	default:
		return fmt.Errorf("unknown policy %q: use standard, strict, off or default", policy)
	}
	return s.updateApp(repo, func(a *config.App) error {
		a.Verify = policy
		return nil
	})
}

// SetWorkflow sets or clears ("" or "none") the workflow that must have built
// a GitHub app's packages: "release.yml" or ".github/workflows/release.yml".
func (s *Store) SetWorkflow(repo, workflow string) error {
	w, err := verify.NormalizeWorkflow(workflow)
	if err != nil {
		return err
	}
	if w != "" {
		r, err := forge.ParseRepo(repo)
		if err != nil {
			return err
		}
		if r.Host != "" {
			return fmt.Errorf("%s is not on GitHub: only GitHub issues build attestations, so a workflow cannot be required", r)
		}
	}
	return s.updateApp(repo, func(a *config.App) error {
		a.Workflow = w
		return nil
	})
}

var errNotListed = errors.New("is not on the list")

func (s *Store) updateApp(repo string, fn func(*config.App) error) error {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return err
	}
	return config.Update(s.ConfigPath, func(c *config.Config) error {
		a := c.Find(repo)
		if a == nil {
			return fmt.Errorf("%s %w (add it first)", repo, errNotListed)
		}
		return fn(a)
	})
}

// Policies is what "lieferling policy" prints and the app menu reads.
type Policies struct {
	Global string      `json:"global"` // "standard" or "strict"
	Apps   []AppPolicy `json:"apps"`   // every app on the list
}

// AppPolicy is one app's policy.
type AppPolicy struct {
	Repo      string `json:"repo"`
	Policy    string `json:"policy"`    // effective: "standard", "strict" or "off"
	Exception bool   `json:"exception"` // set on the app, not inherited
	Workflow  string `json:"workflow"`
	Unmet     string `json:"unmet"` // why strict cannot be met, else ""
}

// Policies lists the global policy and each app's.
func (s *Store) Policies() (Policies, error) {
	cfg, err := config.Load(s.ConfigPath)
	if err != nil {
		return Policies{}, err
	}
	out := Policies{Global: string(verify.Resolve(cfg.Verify, ""))}
	for _, a := range cfg.Apps {
		p := AppPolicy{
			Repo:      a.Repo,
			Policy:    string(verify.Resolve(cfg.Verify, a.Verify)),
			Exception: a.Verify != "",
			Workflow:  a.Workflow,
		}
		if r, err := forge.ParseRepo(a.Repo); err == nil && r.Host != "" && p.Policy == string(verify.Strict) {
			p.Unmet = "not on GitHub and has no build attestations"
		}
		out.Apps = append(out.Apps, p)
	}
	return out, nil
}
