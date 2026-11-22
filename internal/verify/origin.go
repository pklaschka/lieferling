package verify

import (
	"errors"
	"regexp"
	"strings"
)

// Origin is what a verified build attestation says about a package.
type Origin struct {
	Repository string `json:"repository"`       // owner/name
	Workflow   string `json:"workflow"`         // ".github/workflows/release.yml": the workflow that started the run
	Ref        string `json:"ref"`              // "refs/heads/main"
	Commit     string `json:"commit"`           // full SHA
	Signer     string `json:"signer,omitempty"` // "owner/name/.github/workflows/x.yml" when a reusable workflow signed, else ""
}

// WorkflowFile is the workflow's file name, "release.yml".
func (o Origin) WorkflowFile() string {
	if i := strings.LastIndex(o.Workflow, "/"); i >= 0 {
		return o.Workflow[i+1:]
	}
	return o.Workflow
}

// ShortRef is "main" for refs/heads/main, "v1.2.0" for refs/tags/v1.2.0 and
// the ref itself otherwise.
func (o Origin) ShortRef() string {
	for _, p := range []string{"refs/heads/", "refs/tags/"} {
		if s, ok := strings.CutPrefix(o.Ref, p); ok {
			return s
		}
	}
	return o.Ref
}

// ShortCommit is the first seven characters of the commit.
func (o Origin) ShortCommit() string {
	if len(o.Commit) > 7 {
		return o.Commit[:7]
	}
	return o.Commit
}

var workflowPath = regexp.MustCompile(`^\.github/workflows/[^/@]+\.ya?ml$`)

// ErrWorkflowPath is returned by NormalizeWorkflow for a value that names no
// workflow file.
var ErrWorkflowPath = errors.New(`a workflow is a file in .github/workflows, such as "release.yml"`)

// NormalizeWorkflow reads a pin: "" and "none" give ""; "release.yml" gives
// ".github/workflows/release.yml"; a leading "/" is dropped. Anything that
// does not then name a .yml or .yaml file directly in .github/workflows is an
// error.
func NormalizeWorkflow(s string) (string, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "/")
	if s == "" || strings.EqualFold(s, "none") {
		return "", nil
	}
	if !strings.Contains(s, "/") {
		s = ".github/workflows/" + s
	}
	if !workflowPath.MatchString(s) {
		return "", ErrWorkflowPath
	}
	return s, nil
}

// Attested is the check for a verified attestation: pass "verified", or pass
// "pinned" when pinned is set (the workflow was required and matched). The
// report's Origin carries the details for the sentence.
func Attested(source string, o Origin, pinned bool) Check {
	c := Check{ID: CheckAttestation, Result: Pass, Code: "verified", Source: source, Got: o.Repository}
	if pinned {
		c.Code, c.Want = "pinned", o.Workflow
	}
	return c
}

// NotAttested is every other outcome of the attestation check. source is the
// forge's name.
//
//	skip: "unsupported" (the forge has none), "none", "unreadable" (Got: why), "trust" (Got: why)
//	fail: "invalid" (Got: why), "repository" (Want, Got: owner/name), "workflow" (Want, Got: workflow path)
func NotAttested(source, code, want, got string) Check {
	c := Check{ID: CheckAttestation, Result: Skip, Code: code, Source: source, Want: want, Got: got}
	switch code {
	case "invalid", "repository", "workflow":
		c.Result = Fail
	}
	return c
}

// RequireWorkflow appends {policy, fail, "workflow_required", Want: workflow}
// when workflow is set and no attestation check passed or failed (a failed
// one already holds and says why). It appends nothing twice.
func RequireWorkflow(checks []Check, workflow string) []Check {
	if workflow == "" {
		return checks
	}
	for _, c := range checks {
		if c.ID == CheckAttestation && c.Result != Skip {
			return checks
		}
		if c.ID == CheckPolicy && c.Code == "workflow_required" {
			return checks
		}
	}
	return append(checks, Check{ID: CheckPolicy, Result: Fail, Code: "workflow_required", Want: workflow})
}

// SyncedAccepted is digest.synced when the user accepted this digest on
// purpose although another Mac saw a different one: pass "accepted".
func SyncedAccepted(seen, got string) Check {
	return Check{ID: CheckSynced, Result: Pass, Code: "accepted", Want: seen, Got: normalize(got)}
}
