# ADR 0003: Build attestation, signature and quarantine (#30), deliveries 2 and 3 of 3

- **Status:** Proposed
- **Date:** 2026-11-22
- **Decider:** Zuri

## Context

ADR 0002 decided the shape of the customs check and specified delivery 1, which is merged (PR #35): checksum, sender, the synced digests, the `ask` round-trip, a first Zollvermerk and the command line prompt. Two deliveries remain, stacked as one PR each:

2. Build attestation checked with sigstore-go, the policy commands, `lieferling verify <repo>`.
3. Signature and notarization read from the bundle, quarantine kept for notarized apps, the policy controls in the app, the README chapter.

Binding decisions from issue #30 that these deliveries implement:

- The expected identity of an attestation is the app's own repository, optionally one workflow ("only builds from my release workflow").
- `standard`: checksum and attestation are checked when present; a sender change asks. `strict`: checksum **and** attestation are mandatory; a sender change holds. `off`: per app only.
- A signed and notarized app is read with `codesign --verify --deep --strict`, `spctl --assess` and `stapler validate`. If it passes, Lieferling does not run `xattr -cr`: the quarantine flag stays and Gatekeeper checks at first launch. A signature that is present but invalid holds the package.
- Lieferling's own release workflow issues attestations, so `pklaschka/lieferling` can be the first app on `strict`.
- The UI follows the mockup `30-1-zollinhaltserklaerung.png`: the line »ZOLL«, the tag, the box, and the lines Prüfsumme, Herkunft, Absender, Apple-Signatur in that order.

### What delivery 1 built differently from ADR 0002

The plan below starts from the code, not from ADR 0002. The differences that matter:

- `verify.Report` has a field `Sender *Sender`; `Verdict.Stopped()`, `Report.FirstFailure()`, `Report.Stored()` and `verify.NoChecksums(code, file)` exist. There is no `policy.go`; `Resolve` and `Stricter` are in `verify.go`.
- The German summary for `ask` and `held` is the same sentence (»Angehalten, nichts wurde installiert«), and `disputed` is »Widerspruch zwischen deinen Macs«.
- The window does **not** print `Check.Text`. `frontend/src/features/customs.ts` composes its own short German fragments per check code and folds the three digest checks into one line »Prüfsumme«. `Check.Text` is what the command line prints (English) and what tests read. Every new outcome therefore needs two wordings: the sentence in `text.go` and the fragment in `customs.ts`.
- The markup is `details.customs > summary (label, summary, tag, chevron) + .customs-box (ul.customs-lines, p.customs-foot)`, with the template `#customs-line`. The tag counts matching lines (»2 von 2«) or names the line that stopped the package.
- `store.installRelease` ends in `held(...)` or `finish(...)`; `record` writes digests with `verify.Merge`.
- `install.Inspect` is `func(ctx, Unpacked) error`; `identify` runs only when `inspect != nil`.
- **`config.App` has no field for the workflow pin.** ADR 0002 said all config fields of the three deliveries were defined in delivery 1. That was wrong for the pin; see "The workflow pin" below.

## Evidence gathered for this ADR

Everything in this section was measured or read on 2026-11-22. What could not be verified is listed at the end of the ADR.

### sigstore-go

- Current version: `github.com/sigstore/sigstore-go v1.4.0` (2026-10-29). Its `go.mod` says `go 1.27.0`; v1.3.0 and v1.2.2 say `go 1.25.8`. The GitHub CLI v2.105.0 (2026-11-17) uses v1.4.0.
- API, read from the module source and exercised in a scratch test against a real bundle:
  - `root.NewTrustedRootFromJSON(b)` or `root.FetchTrustedRootWithOptions(opts)` gives the `root.TrustedMaterial`.
  - `verify.NewVerifier(trusted, opts...)`. For the public Sigstore instance: `verify.WithSignedCertificateTimestamps(1), verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1)`. For GitHub's own instance: `verify.WithSignedTimestamps(1)` (it has a timestamp authority and no transparency log). These are the options `gh attestation verify` uses.
  - `verifier.Verify(&bundle, verify.NewPolicy(verify.WithArtifactDigest("sha256", raw), verify.WithCertificateIdentity(id)))`, with `id` from `verify.NewCertificateIdentity(sanMatcher, issuerMatcher, certificate.Extensions{...})`. A SAN criterion and an issuer criterion are both required.
  - The result carries `Signature.Certificate` (a `certificate.Summary`): `SubjectAlternativeName`, `Issuer`, `SourceRepositoryURI`, `SourceRepositoryRef`, `SourceRepositoryDigest`, `BuildSignerURI`, `BuildConfigURI`, `RunnerEnvironment`; and `Statement.PredicateType`.
  - A bundle decodes with `json.Unmarshal` into `bundle.Bundle`. Verification itself is offline; only the trust root needs the network.
- Scratch test results (bundle `sigstore-js-2.1.0-bundle.json` and a reusable-workflow bundle from the GitHub CLI's test data, trust root `examples/trusted-root-public-good.json` from sigstore-go, no network):
  - correct digest and repository: verified; the summary gave `release.yml@refs/heads/main`, repository, ref and commit;
  - wrong `SourceRepositoryURI`: `failed to verify certificate identity: ... expected SourceRepositoryURI to be ..., got ...`;
  - wrong digest: `provided artifact digest does not match any digest in statement`;
  - **reusable workflow:** the SAN and `BuildSignerURI` name the reusable workflow in *another* repository (`github/artifact-attestations-workflows/.github/workflows/attest.yml@<sha>`), while `SourceRepositoryURI` is the calling repository and `BuildConfigURI` is the workflow in the calling repository (`.../.github/workflows/shared.yml@refs/heads/main`). So "the app's own repository" must be checked on `SourceRepositoryURI`, and "my release workflow" on `BuildConfigURI`, not on the SAN.
- Trust roots:
  - Public instance: `tuf.DefaultOptions()` uses an embedded `root.json`, the mirror `https://tuf-repo-cdn.sigstore.dev` and the cache `~/.sigstore/root`. `CacheValidity` is 0 by default, which refreshes on every client creation. With `CacheValidity = 1` the cached metadata is used for a day. `tuf.New` first reads the cache in local mode and falls back to a full refresh; with no cache and no network it fails. The embedded root is version 14 with `expires` 2026-06-22, so a first use always needs the network to walk the root chain forward.
  - GitHub's instance (private repositories): the GitHub CLI embeds a second TUF root (`pkg/cmd/attestation/verification/embed/tuf-repo.github.com/root.json`, version 3), uses the mirror `https://tuf-repo.github.com` and reads the target `trusted_root.json` (`<trust domain>.trusted_root.json` only for GitHub Enterprise tenants on `ghe.com`, which Lieferling does not support). It picks the verifier by the leaf certificate's issuer organization: `sigstore.dev` or `GitHub, Inc.`.
  - `gh` sets `CacheValidity = 1`, its own cache folder, and three fetch attempts.
- Cost, measured with `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "-s -w"` (the app with `-tags purego`), both sides built with Go 1.27.0, the "after" side with a minimal `internal/attest` that fetches both trust roots and verifies a bundle:

  | | before | after | difference |
  |---|---|---|---|
  | command line, binary | 7,149,282 B | 17,963,762 B | +10.8 MB (x2.5) |
  | command line, gzip | 2,955,855 B | 6,563,305 B | +3.6 MB |
  | app binary | 12,071,778 B | 21,371,346 B | +9.3 MB (x1.8) |
  | app binary, gzip | 4,887,002 B | 7,955,943 B | +3.1 MB |
  | modules compiled into the command line | 2 | 68 | +66 |
  | modules compiled into the app | 6 | 70 | +64 |
  | modules in the build list (`go list -m all`) | 147 | 479 | +332 |
  | `go.sum` lines | 25 | 361 | +336 |
  | `go` directive | 1.26.0 | 1.27.0 | forced by v1.4.0 |

  The universal DMG (18.3 MB today) grows by about 6 MB, each command line archive (2.7 MB) by about 3.6 MB. The tree brings go-tuf, the Rekor and timestamp clients, go-openapi, protobuf, gRPC status types and OpenTelemetry API packages. `go vet` and `go test ./internal/... ./cmd/...` pass with the dependency and Go 1.27.0, and the app cross-builds.

### GitHub attestations API

- `GET /repos/{owner}/{repo}/attestations/{subject_digest}` with the digest written `sha256:<hex>`. The GitHub CLI sends `per_page` (at most 100, default 30) and `predicate_type`, follows the `Link` header for more pages, retries on 5xx, and decodes `{"attestations":[{"bundle":{...},"bundle_url":"...","initiator":"..."}]}`. When `bundle_url` is set it fetches the bundle from there (snappy-compressed JSON, fetched without the API token) and otherwise uses the inline `bundle`.
- Probe against `pklaschka/lieferling` (v0.11.0, `Lieferling-0.11.0-universal.dmg`, digest `sha256:c7b6e765…fa0eb` as the release API lists it): **`404 Not Found`** with `{"message":"Not Found","documentation_url":"https://docs.github.com/rest/repos/attestations#list-attestations","status":"404"}`. So "no attestation" is a 404 here, not an empty list. A malformed digest and a digest without the `sha256:` prefix also answer 404. The request counted against the `core` rate limit.
- The same request, sent with `X-GitHub-Api-Version: 2022-11-28` as `forge.GitHub.request` does, came back with `Deprecation: Tue, 10 Mar 2026` and `Sunset: Fri, 10 Mar 2028`. Not part of #30; noted for a later change of that header.
- Probe against `cli/cli`: not possible. This session's proxy only allows the GitHub API for this repository (`403 GitHub access to this repository is not enabled for this session`). The response shape above is therefore taken from the GitHub CLI's client code and its tests, not from a live answer.
- Forgejo and Gitea: `internal/forge/gitea.go` has no attestation call, the Gitea release API has no digest field (ADR 0002), and the issue lists "Forgejo/Gitea have none" as a known limit. The line for these forges is "not checkable". `codeberg.org` was not reachable from here to probe.
- Tokens: `forge.GitHub.request` sends `Authorization: Bearer <token>` when `GitHubToken()` finds one, to `api.github.com` only. The attestation call goes through the same function.

### Menu API

Wails 3.1.0 has `(*Menu).AddSubmenu`, `AddRadio(label, checked)` and `(*MenuItem).SetChecked`, which unchecks the other members of the radio group. `main.go` builds the app menu by hand (`appMenu.AddSubmenu("Lieferling")`), so a submenu is a few lines.

## Decision

1. **sigstore-go v1.4.0 is accepted, in a new package `internal/attest`.** `internal/verify` stays a leaf on the standard library. The cost (about +10 MB per binary, +6 MB on the DMG, 66 more modules, Go 1.27) is high for one check, and there is no cheaper honest option: verifying a Sigstore bundle means X.509 chains, certificate transparency, a Merkle inclusion proof or an RFC 3161 timestamp, DSSE and TUF. Writing that by hand, or shelling out to `gh` (often not installed, never for Forgejo users), would be worse. See "Options considered".
2. **The forge call lives in `internal/forge`** (`GitHub.Attestations`), the verification in `internal/attest`, the decision and wording in `internal/verify`, the orchestration in `internal/store` behind an interface `store.Attester`, so the store tests need neither network nor sigstore-go.
3. **Expected identity:** issuer `https://token.actions.githubusercontent.com`, `SourceRepositoryURI` equal to `https://github.com/<owner>/<repo>` without case, predicate type `https://slsa.dev/provenance/v1`. The optional pin compares the workflow path in `BuildConfigURI`.
4. **The pin is a new config field `App.Workflow`**, and a pin makes the attestation mandatory for that app under every policy except `off`.
5. **`strict` adds "a verified attestation is mandatory".** Apps on Forgejo and Gitea cannot meet it and need a per-app `standard`.
6. **Delivery 2 ships the command line (`policy`, `verify`, `--accept-digest`), the line »Herkunft« in the window, and the attestation step in Lieferling's release workflow.** Delivery 3 ships signature, notarization, quarantine, the menu and the row entry, and the README chapter.
7. **Lieferling sets the quarantine flag itself for a notarized app** (nothing it downloads carries one), and never touches the extended attributes of such an app otherwise. This is the part with the most unknowns; it is gated on hand checks, with a fallback (Open point 1).
8. **No fourth stop.** The mockup has three stops and the line »ZOLL« directly under them.

### Options considered for the attestation check

| | A: sigstore-go in `internal/attest` (chosen) | B: shell out to `gh attestation verify` | C: verify by hand with the standard library |
|---|---|---|---|
| Works without extra tools | yes | no: needs `gh`, logged in | yes |
| Binary size | +9 to +11 MB | 0 | about 0 |
| Trust root handling | TUF, maintained upstream | `gh`'s | none: pinned certificates that rot |
| Correctness risk | upstream, used by `gh` and cosign | upstream | ours, in security code |
| Testable offline | yes, with a fixture root | no | yes |
| Issue #30 | names sigstore-go | no | no |

A fallback inside A, if Go 1.27 turns out to hurt the Wails build on the release runner: pin sigstore-go v1.3.0 (`go 1.25.8`), which has the same API surface used here. The cross-build with 1.27.0 worked in the trial, so start with v1.4.0.

## Delivery 2 in detail: attestation, policy commands, `lieferling verify`

### Flow

```
store.installRelease (unchanged around it)
  download, re-read the list, resolve the policy          (as today)
  checkDownload:
    digest.forge, digest.checksums, digest.synced         (as today; digest.synced may be "accepted")
    if none of them failed:
      attestation                                         new
        not on GitHub                    -> skip unsupported
        forge.Attestations(repo, digest) -> none: skip none; error: skip unreadable
        Attester.Verify(bundles, expect) -> pass verified|pinned, fail invalid|repository|workflow, skip trust
    verify.RequireWorkflow                                 a pin without a verified attestation: policy fail
  verify.Decide                                           now here too: Held stops before the archive is opened
  Installer.Install(file, name, inspect)                  sender, Decide again (as today)
```

A failing attestation holds under `standard` too: an attestation that is present and wrong is a contradiction, like a wrong checksum. Only its absence is tolerated by `standard`.

### `internal/verify`

```go
const CheckAttestation = "attestation" // delivery 3 adds CheckSignature, CheckNotarization

// Origin is what a verified build attestation says about a package.
type Origin struct {
	Repository string `json:"repository"`       // owner/name
	Workflow   string `json:"workflow"`         // ".github/workflows/release.yml": the workflow that started the run
	Ref        string `json:"ref"`              // "refs/heads/main"
	Commit     string `json:"commit"`           // full SHA
	Signer     string `json:"signer,omitempty"` // "owner/name/.github/workflows/x.yml" when a reusable workflow signed, else ""
}

func (o Origin) WorkflowFile() string // "release.yml"
func (o Origin) ShortRef() string     // "main" for refs/heads/main, "v1.2.0" for refs/tags/v1.2.0, else Ref
func (o Origin) ShortCommit() string  // first 7 characters

// Report gets:
	// Origin is set when a build attestation was verified.
	Origin *Origin `json:"origin,omitempty"`

// NormalizeWorkflow reads a pin: "" and "none" give ""; "release.yml" gives
// ".github/workflows/release.yml"; a leading "/" is dropped. Anything that
// does not then match ^\.github/workflows/[^/@]+\.ya?ml$ is an error.
func NormalizeWorkflow(s string) (string, error)

// Attested is the check for a verified attestation: pass "verified", or pass
// "pinned" when pinned is set (the workflow was required and matched).
func Attested(source string, o Origin, pinned bool) Check

// NotAttested is every other outcome. source is the forge's name.
//   skip: "unsupported" (forge has none), "none", "unreadable" (Got: why), "trust" (Got: why)
//   fail: "invalid" (Got: why), "repository" (Want, Got: owner/name), "workflow" (Want, Got: workflow path)
func NotAttested(source, code, want, got string) Check

// RequireWorkflow appends {policy, fail, "workflow_required", Want: workflow}
// when workflow is set and no attestation check passed or failed. (A failed
// one already holds and says why.)
func RequireWorkflow(checks []Check, workflow string) []Check

// SyncedAccepted is digest.synced when the user accepted this digest on
// purpose: pass "accepted" (Want seen, Got got).
func SyncedAccepted(seen, got string) Check
```

`Decide` becomes:

```go
//	any failing check other than sender       -> Held
//	strict and neither digest.forge nor
//	  digest.checksums passed                 -> append {policy, fail, "checksum_required"}
//	strict and attestation did not pass       -> append {policy, fail, "attestation_required"}
//	either appended                           -> Held
//	sender fail, standard                     -> Ask
//	sender fail, strict                       -> Held
//	otherwise: a pass among digest.*,
//	  attestation, sender                     -> Cleared, else Unchecked
```

Both policy lines are appended when both are missing, so the note names everything `strict` lacks at once. `Decide` must stay idempotent, because the store now calls it twice (after `checkDownload` and inside inspect): it does not append a `policy` line that is already there. "A pass among digest.*, attestation, sender" is written out on purpose: in delivery 3 a valid signature must not turn `unchecked` into »Geprüft und freigegeben«.

`Localize` passes the report to `sentence`, which fills four more placeholders from `r.Origin` when it is set: `{workflow}` (`Origin.Workflow`, plus " (signed by " + `Signer` + ")" / " (signiert von …)" when `Signer` is set), `{ref}` (`ShortRef`), `{commit}` (`ShortCommit`), `{repo}`.

#### Wording (`text.go`)

No new verdict and no new summary. New sentences; "build attestation" is »Herkunftsnachweis« in German:

| ID | Result, code | English | German |
|---|---|---|---|
| `attestation` | pass `verified` | GitHub holds a signed build attestation for exactly this file: built by {workflow} from {ref} at commit {commit} in {repo}. This shows where the file was built, not that its code is harmless. | GitHub führt für genau diese Datei einen signierten Herkunftsnachweis: gebaut von {workflow} aus {ref} beim Commit {commit} in {repo}. Das zeigt, wo die Datei gebaut wurde, nicht, dass ihr Code harmlos ist. |
| | pass `pinned` | GitHub holds a signed build attestation for exactly this file: built by {workflow} from {ref} at commit {commit} in {repo}. That is the workflow you require. | GitHub führt für genau diese Datei einen signierten Herkunftsnachweis: gebaut von {workflow} aus {ref} beim Commit {commit} in {repo}. Das ist der Workflow, den du verlangst. |
| | fail `invalid` | GitHub holds a build attestation for this file, but its signature could not be confirmed. | GitHub führt für diese Datei einen Herkunftsnachweis, aber seine Signatur ließ sich nicht bestätigen. |
| | fail `repository` | The build attestation for this file comes from another repository: expected {want}, found {got}. | Der Herkunftsnachweis für diese Datei stammt aus einem anderen Repository: erwartet {want}, gefunden {got}. |
| | fail `workflow` | This file was built by {got}, and you require {want}. | Diese Datei wurde von {got} gebaut, und du verlangst {want}. |
| | skip `none` | {source} holds no build attestation for this file. | {source} führt für diese Datei keinen Herkunftsnachweis. |
| | skip `unsupported` | {source} issues no build attestations, so where this file was built cannot be checked. | {source} stellt keine Herkunftsnachweise aus, wo diese Datei gebaut wurde, lässt sich also nicht prüfen. |
| | skip `unreadable` | {source} did not hand out the build attestations for this file. | {source} hat die Herkunftsnachweise für diese Datei nicht herausgegeben. |
| | skip `trust` | A build attestation exists, but the Sigstore trust root needed to check it could not be loaded. | Es gibt einen Herkunftsnachweis, aber die Sigstore-Vertrauensanker zum Prüfen ließen sich nicht laden. |
| `policy` | fail `attestation_required` | The strict policy requires a confirmed build attestation and there is none. | Die strenge Kontrolle verlangt einen bestätigten Herkunftsnachweis, es gibt keinen. |
| | fail `workflow_required` | You require builds from {want} for this app, and no build attestation could be confirmed. | Du verlangst für diese App Builds aus {want}, und es ließ sich kein Herkunftsnachweis bestätigen. |
| `digest.synced` | pass `accepted` | One of your Macs saw a different package for this version, and you accepted this one. | Einer deiner Macs hat für diese Version ein anderes Paket gesehen, und du hast dieses angenommen. |

None of these may contain "Apple", "notar" or "Gatekeeper"; `TestWording` keeps checking that for every ID except the two of delivery 3. "Signed" here is Sigstore's signature, and the sentence says who holds the attestation (GitHub), so it cannot read as a statement about the app's code signature.

### `internal/forge`

```go
// ErrNoAttestations is returned for a forge that has no build attestations
// (Forgejo, Gitea).
var ErrNoAttestations = errors.New("this forge offers no build attestations")

// Attestations returns the Sigstore bundles (as JSON) that GitHub holds for
// the file with this digest ("sha256:…") in owner/repo; nil when it holds none.
func (c *GitHub) Attestations(ctx context.Context, repo, digest string) ([][]byte, error)

// Attestations is GitHub.Attestations for GitHub repositories and
// ErrNoAttestations for all others.
func (c *Client) Attestations(ctx context.Context, repo, digest string) ([][]byte, error)
```

- Request: `c.request(ctx, base+"/repos/"+repo+"/attestations/"+digest+"?per_page=30&predicate_type="+url.QueryEscape("https://slsa.dev/provenance/v1"), "application/vnd.github+json")`. Reject a digest that is not `sha256:` plus 64 hex digits before building the URL.
- `ErrNotFound` (the measured answer for "none") gives `nil, nil`. An empty `attestations` array gives `nil, nil` as well.
- Decode `struct{ Attestations []struct{ Bundle json.RawMessage \`json:"bundle"\`; BundleURL string \`json:"bundle_url"\` } }`, body limited to 8 MiB. Per entry: use `Bundle` when it is neither empty nor `null`. Otherwise, when `BundleURL` is an `https` URL, fetch it **without the Authorization header** (it is another host), at most 4 MiB, and decode it with snappy (`github.com/golang/snappy`, one small module more; `gh` prefers this path). An entry with neither is skipped. Only the first page is read: more than 30 provenance attestations for one file is not a case worth a loop.
- Private repositories need a token that may read attestations (a classic token with `repo`; a fine-grained token with the repository permission "Attestations: read"). A `403` ends as skip `unreadable`; the README says which permission is missing.

### `internal/attest` (new, the only package that imports sigstore-go)

Files: `attest.go`, `roots.go`, `identity.go`, `attest_test.go`, `tuf/github-root.json`, `testdata/`.

```go
// Expect is what an attestation must say to count.
type Expect struct {
	Digest   string // "sha256:<hex>" of the downloaded file
	Repo     string // owner/name on github.com
	Workflow string // normalized pin, or "" for any workflow of the repository
}

var (
	ErrTrust      = errors.New("the Sigstore trust root could not be loaded")
	ErrInvalid    = errors.New("no attestation with a valid signature for this file")
	ErrRepository = errors.New("the attestation names another repository")
	ErrWorkflow   = errors.New("the attestation names another workflow")
)

// Mismatch wraps ErrRepository or ErrWorkflow with what was expected and found.
type Mismatch struct {
	Kind      error
	Want, Got string
}

// Verifier checks build attestations against Sigstore's public instance and
// GitHub's own. The zero value is not usable; use New.
type Verifier struct {
	CacheDir string       // TUF cache; "" disables the local cache
	HTTP     *http.Client // for TUF; nil means a client with a 30 s timeout
	// roots overrides the trust roots by issuer organization (tests).
	roots map[string]root.TrustedMaterial
	mu    sync.Mutex
}

func New(cacheDir string) *Verifier

// Verify returns the origin of the first bundle that has a valid signature
// for want.Digest, was built in want.Repo and, when want.Workflow is set, by
// that workflow. It never returns an origin together with an error.
func (v *Verifier) Verify(ctx context.Context, bundles [][]byte, want Expect) (verify.Origin, error)
```

`Verify`, per bundle:

1. `json.Unmarshal` into `bundle.Bundle`; a bundle that does not parse counts as invalid.
2. Read the leaf certificate (`b.VerificationContent()`, `.Certificate()`), take `Issuer.Organization[0]`: `sigstore.dev` or `GitHub, Inc.`; anything else is invalid.
3. `material(ctx, org)`: the cached trust root for that instance, loaded on first use (see below). An error here ends the whole call with `ErrTrust`: no bundle can be judged without it.
4. `verify.NewVerifier(material, options for that instance)` and `Verify` with `WithArtifactDigest("sha256", raw)` and an identity of issuer `https://token.actions.githubusercontent.com` (exact) and the SAN regular expression `^https://github\.com/`. The repository is deliberately not in the sigstore policy: sigstore-go compares extensions with case, GitHub does not, and the comparison below gives a better message.
5. `res.Statement.PredicateType` must be `https://slsa.dev/provenance/v1`, else invalid.
6. `strings.EqualFold(cert.SourceRepositoryURI, "https://github.com/"+want.Repo)`, else remember `Mismatch{ErrRepository, want.Repo, <owner/name from the URI>}`.
7. `workflowOf(cert.BuildConfigURI, cert.SourceRepositoryURI)` gives the workflow path and the ref (fall back to `BuildSignerURI` when `BuildConfigURI` is empty). When `want.Workflow` is set and differs (with case: file names on GitHub have case), remember `Mismatch{ErrWorkflow, want.Workflow, path}`.
8. Otherwise return `verify.Origin{Repository, Workflow: path, Ref: cert.SourceRepositoryRef, Commit: cert.SourceRepositoryDigest, Signer}`. `Signer` is the SAN without `https://github.com/` and without its `@ref`, set only when its repository or path differs from the workflow.

If no bundle passed, return the most specific error seen: `ErrWorkflow` before `ErrRepository` before `ErrInvalid` (wrapping sigstore-go's last error text).

`identity.go` holds the pure part, tested without bundles:

```go
// workflowOf splits ".../.github/workflows/release.yml@refs/heads/main" (after
// the repository URL, compared without case) into path and ref. A workflow
// file name holds no "/", and a ref is "refs/…" or a commit ID, so the cut is
// the last "@" before the first "/" that follows ".github/workflows/", or the
// last "@" when no "/" follows.
func workflowOf(uri, repoURI string) (path, ref string, ok bool)
```

`roots.go`:

```go
//go:embed tuf/github-root.json
var githubRoot []byte // from cli/cli (MIT), pkg/cmd/attestation/verification/embed/tuf-repo.github.com/root.json

func (v *Verifier) material(ctx context.Context, org string) (root.TrustedMaterial, error)
```

- `sigstore.dev`: `tuf.DefaultOptions().WithContext(ctx).WithCachePath(v.CacheDir).WithCacheValidity(1)`, then `root.FetchTrustedRootWithOptions`.
- `GitHub, Inc.`: the same options plus `WithRoot(githubRoot).WithRepositoryBaseURL("https://tuf-repo.github.com")`, `tuf.New`, `GetTarget("trusted_root.json")`, `root.NewTrustedRootFromJSON`.
- With `CacheDir == ""`: `WithDisableLocalCache()`.
- The fetcher: `fetcher.NewDefaultFetcher()` with `SetHTTPClient(v.HTTP)`; the default has no deadline suited to an install.
- Each instance is loaded at most once per process and only when a bundle needs it, so a Mac that only has public apps never contacts `tuf-repo.github.com`.

`store.New` sets `Attest: attest.New(filepath.Join(<user cache dir>, "Lieferling", "sigstore"))`, next to the icon cache.

Offline behaviour, stated plainly: the first attestation check on a Mac needs `tuf-repo-cdn.sigstore.dev` (or `tuf-repo.github.com`). After that the cache answers for a day without the network, and for as long as the cached TUF metadata is valid when a refresh fails. An install always has the network, since it just downloaded the package; the case that matters is a firewall that allows GitHub and blocks the TUF hosts. That ends as skip `trust`: tolerated by `standard`, held by `strict`.

### `internal/config`: the workflow pin

```go
type App struct {
	// … as today …
	// Workflow, when set, is the workflow that must have built this app's
	// packages (".github/workflows/release.yml"). GitHub only. It makes a
	// verified build attestation mandatory for this app.
	Workflow string `json:"workflow,omitempty"`
}
```

- Merge rule for conflict copies: the main file's value wins; an empty one is filled from the copy. Two different pins keep the main file's. Either way a pin never vanishes in a merge.
- **Correction to ADR 0002.** That ADR promised that delivery 1 defined every config field, so that no released version would strip a later one. `workflow` is new, so a Mac still running a delivery 1 build removes the pin whenever it rewrites the list, and the app then falls back to "any workflow of the repository". This is the same weakness ADR 0002 accepted for Macs older than delivery 1, and it has the same cure: every Mac updates. It degrades silently, which is worse for a pin than for a digest, so: the release notes of delivery 2 say it, `lieferling policy` prints the pins so a missing one is visible, and no further config field is added in delivery 3 (it needs none).
- A catalog export still carries neither policy nor pin.

### `internal/store`

```go
// Attester checks build attestations. attest.Verifier in the app; a fake in tests.
type Attester interface {
	Verify(ctx context.Context, bundles [][]byte, want attest.Expect) (verify.Origin, error)
}

type Store struct {
	// … as today …
	Attest Attester // nil: attestations count as not checkable ("trust")
}

// Accept is what the user agreed to before an install.
type Accept struct {
	Sender bool   // the sender change waiting in state.Held (as AcceptSender)
	Digest string // "sha256:…": install this package although the synced list holds another digest for its version
}

// InstallAccepting is Install with acceptances. AcceptSender(ctx, repo, p)
// stays and calls InstallAccepting(ctx, repo, Accept{Sender: true}, p).
func (s *Store) InstallAccepting(ctx context.Context, repo string, a Accept, progress Progress) (Status, error)

// VerifyOptions for Verify.
type VerifyOptions struct {
	Strict bool // judge by the strict policy whatever the list says
}

// Verify downloads the newest package of repo and runs the whole customs
// check, including unpacking it and reading the sender, and installs nothing.
// It writes neither state.json nor config.json. The error wraps verify.ErrHeld
// or verify.ErrAsk when the package would not be installed without a question.
func (s *Store) Verify(ctx context.Context, repo string, o VerifyOptions, progress Progress) (verify.Report, error)

// SetPolicy sets the policy for all apps (repo "") to "standard" or "strict",
// or an app's exception to "standard", "strict", "off" or "default" (none).
func (s *Store) SetPolicy(repo, policy string) error

// SetWorkflow sets or clears ("" or "none") the workflow pin of a GitHub app.
func (s *Store) SetWorkflow(repo, workflow string) error

// Policies is what "lieferling policy" prints and the app menu reads.
type Policies struct {
	Global string        // "standard" or "strict"
	Apps   []AppPolicy   // every app on the list
}
type AppPolicy struct {
	Repo      string
	Policy    string // effective: "standard", "strict" or "off"
	Exception bool   // set on the app, not inherited
	Workflow  string
	Unmet     string // why strict cannot be met ("not on GitHub"), else ""
}
func (s *Store) Policies() (Policies, error)
```

Changes in `install.go` and `customs.go`:

- `install(ctx, repo, progress, accepted *verify.Report)` becomes `install(ctx, repo, progress, acc accepted, dry *VerifyOptions)` with `type accepted struct { sender *verify.Report; digest string }`. `InstallAccepting` loads the held report for `Accept.Sender` exactly as `AcceptSender` does today.
- `checkDownload` gets the report to fill and the accepted digest: `checkDownload(ctx, app, rel, asset, rep *verify.Report, acceptDigest string)`.
  - `digest.synced`: when `SyncedDigest` fails and `acceptDigest` equals the download's digest (compared like `sameDigest`), use `verify.SyncedAccepted`. An accepted digest that is not the downloaded one changes nothing.
  - then, only when no digest check failed, `s.attestationCheck`, and `verify.RequireWorkflow(checks, app.Workflow)`.
- `attestationCheck(ctx, app, digest) (verify.Check, *verify.Origin)`:
  1. `forge.ParseRepo`; `Host != ""`: `NotAttested(forgeName, "unsupported", "", "")`.
  2. `s.Forge.Attestations`: error: `unreadable` with the error text in `Got`; none: `none`.
  3. `s.Attest == nil`: `trust`. Else `Verify(ctx, bundles, attest.Expect{Digest, Repo: r.Path(), Workflow: app.Workflow})`.
  4. Map the error: `ErrTrust` to skip `trust`; `Mismatch` with `ErrRepository` or `ErrWorkflow` to fail with `Want`/`Got`; anything else to fail `invalid`. No error: `Attested("GitHub", origin, app.Workflow != "")` and the origin for `rep.Origin`.
- After `checkDownload`: `rep.Verdict, rep.Checks = verify.Decide(rep.Policy, rep.Checks)`; `Held` goes to `s.held(...)` (this replaces today's `if rep.Failed()`), so a missing attestation under `strict` never mounts the image.
- `record`: when `digest.synced` was accepted, write `a.Digests[k] = rep.Digest` instead of merging, which ends the dispute for this version in favour of the accepted package.
- Dry run (`Verify`): policy `off` returns `{Verdict: Exempt}` without downloading, unless `Strict`. Otherwise the same path with three differences: the inspect closure ends with a sentinel `errVerified` when the verdict is neither `ask` nor `held`, so `place` never runs; nothing calls `hold`, `record` or `config.Update`; the result is the localized report. `Strict` replaces `rep.Policy`.
- `SetPolicy` validates with the values above (`off` and `default` only with a repo; unknown values are an error here, even though `Resolve` would read them as strict). `SetWorkflow` uses `verify.NormalizeWorkflow` and refuses a repository that is not on GitHub.
- `Status` gets `Policy string \`json:"policy"\`` (the app's own exception, "" for none); delivery 3's row dialog reads it.

Per-app `off` is unchanged: no attestation call, nothing written.

### `service.go` and the window

- No new binding method in delivery 2. `Status.customs.origin` and the new check arrive through the regenerated bindings; `backend.ts` adds `Origin` to the type export.
- `customs.ts` gets one more line, in the mockup's order: **Prüfsumme, Herkunft, Absender**. `linesOf` inserts `originLine(r, checks)` after the checksum line whenever the report has an `attestation` check or a `policy` check with code `attestation_required` or `workflow_required`. Reports stored by delivery 1 have neither and keep their two lines.
- The tag needs no change: three lines, »3 von 3« in delivered green as in the mockup, »2 von 3« in `ink-2` when there is no attestation, »HERKUNFT« in express red when the line stopped the package.
- The line, with the same pieces the other lines use (text in `ink-2`, `code()` for mono ink, `.changed` for red words):

  | Outcome | Icon | »Herkunft« reads |
  |---|---|---|
  | pass `verified`, `pinned` | tick | gebaut von `release.yml` aus `main` @ `4c1d9e0` (exactly the mockup; from `origin.workflow` base name, short ref, short commit) |
  | skip `none` | dots | kein Herkunftsnachweis bei GitHub |
  | skip `unsupported` | dots | {Forge} stellt keine Herkunftsnachweise aus |
  | skip `unreadable` | dots | GitHub hat den Herkunftsnachweis nicht herausgegeben |
  | skip `trust` | dots | nicht prüfbar, die Sigstore-Vertrauensanker waren nicht erreichbar |
  | fail `invalid` | alert | die Signatur des Herkunftsnachweises ließ sich nicht bestätigen |
  | fail `repository` | alert | gebaut in `other/repo`, erwartet `owner/repo` |
  | fail `workflow` | alert | gebaut von `build.yml`, verlangt ist `release.yml` |
  | policy `attestation_required` | alert | kein bestätigter Herkunftsnachweis, die strenge Kontrolle verlangt einen |
  | policy `workflow_required` | alert | kein bestätigter Herkunftsnachweis, verlangt ist `release.yml` |

- The foot of a held note stays »Lieferling hat die Datei verworfen.«
- `checksumLine`: `synced` pass `accepted` reads »`sha256 9f2c…e41a` von dir angenommen, ein anderer Mac hat ein anderes Paket gesehen« when nothing else matched, and is left out of the "stimmt mit" list otherwise.
- No new markup, no new CSS, no new token. `DESIGN.md` gets one sentence in "Customs note" for the line »Herkunft«.

### Command line

```
lieferling policy                                   show the policy, the exceptions and the pins
lieferling policy standard|strict                   set the policy for all apps
lieferling policy <repo> standard|strict|off|default
lieferling policy <repo> [<policy>] --workflow <file>|none
lieferling verify <repo> [--strict] [--json]        check the newest package, install nothing
lieferling install <repo> [--accept-sender] [--accept-digest sha256:…]
lieferling update <repo> [--accept-sender] [--accept-digest sha256:…]
```

- `policy` with one word that holds no "/" sets the global policy; a repository always holds one, so there is no ambiguity.
- `lieferling policy` prints:

  ```
  Policy for all apps: standard
  REPO                         POLICY      WORKFLOW
  pklaschka/lieferling         strict      .github/workflows/release.yml
  pklaschka/feldspur           (standard)  -
  codeberg.org/zuri/werkbank   (standard)  -
  ```

  An inherited policy is in brackets. Under a global `strict`, each app with `Unmet` gets a line after the table: `codeberg.org/zuri/werkbank is not on GitHub and has no build attestations: strict will hold it. Exempt it with: lieferling policy codeberg.org/zuri/werkbank standard`. Setting `strict` prints the same lines at once.
- `verify` prints what `install` prints (`Customs: <summary>` and one line per check with `ok`, `FAIL` or `--`) and then one of `Would install <name> <version>.`, `Would ask before installing: the sender changed.` or `Would not install <name> <version>.` With `--json` it prints only the localized report as indented JSON and no progress.
- Exit codes: `0` for `cleared`, `unchecked` and `exempt`; `3` for `held` and `ask` (the existing `exitCode`, because `Verify` wraps `verify.ErrHeld` or `verify.ErrAsk`); `1` for any other error (no release, network); `2` for usage. `unchecked` is `0` because nothing contradicted; a script that wants more passes `--strict`.
- `--accept-digest` takes a full `sha256:<64 hex>`; anything else is a usage error. It is refused for `update` without exactly one repository. `install` passes it in `store.Accept` and keeps it for the second call after a sender prompt (`Accept{Sender: true, Digest: d}`), so the two acceptances combine.
- `reorder` in `main.go` splits arguments by a leading "-", which separates `--workflow` from `release.yml`. Give it a set of flags that take a value (`workflow`, `accept-digest`): for those, the next argument stays with the flag unless the flag contains "=".
- `help`, `usage()` and the package comment get the three additions and one paragraph on `policy`.

#### Decision on `--accept-digest` (open point of ADR 0002)

Yes, in delivery 2, on the command line only. A developer who re-uploads an asset under the same tag after one Mac installed it produces a permanent `digest.synced` contradiction, and today the only ways out are a new release, switching the check off for the app, or editing `config.json` in iCloud by hand. The last two are worse than a narrow override:

- It overrides `digest.synced` only. `digest.forge`, `digest.checksums` and `attestation` still have to pass or be absent, so under `strict` the re-uploaded file still needs GitHub's digest and a valid attestation.
- It names the package by its full digest, which `lieferling verify` prints, so it cannot accept "whatever comes".
- The report records it (`digest.synced` pass `accepted`) and the synced entry is replaced, so the other Macs stop holding that version.

The window offers no button for it. The held note keeps saying what happened; a contested digest is rare and deserves the deliberate path.

### Lieferling's own release workflow (in delivery 2)

It belongs here: without attested releases of Lieferling itself there is nothing to try the feature on, and the delivery 2 merge is the first release built with the changed workflow.

`.github/workflows/release.yml`:

```yaml
permissions:
  contents: write
  id-token: write       # the OIDC token Sigstore signs for
  attestations: write   # store the attestation with the repository
```

and, after "Build the command line" and **before** "Publish the release", so a release never appears without its attestations:

```yaml
      - name: Attest the release files
        if: steps.version.outputs.version != ''
        uses: actions/attest-build-provenance@v4
        with:
          subject-path: dist/*
```

The subjects are the DMG, the ZIP and the two command line archives, exactly the files Lieferling later downloads, so the digests match. Its own commit, `ci: attest release files`, inside the delivery 2 PR. After the first attested release: `lieferling verify pklaschka/lieferling --strict` must exit 0, then `lieferling policy pklaschka/lieferling strict --workflow release.yml`.

### README (delivery 2)

Extend the "Customs" bullet under "How it picks what to install" by the attestation sentence and add `policy` and `verify` to the "Command line" list. The full chapter is delivery 3.

## Delivery 3 in detail: signature, notarization, quarantine, controls, README

### What counts as what

| State | How it is recognised | Line »Apple-Signatur« | Quarantine | Verdict |
|---|---|---|---|---|
| not signed | `codesign -dv` says "code object is not signed at all" | not signed | cleared (`xattr -cr`, as today) | unaffected |
| ad hoc | `Signature=adhoc` | not signed | cleared | unaffected |
| signed, valid, notarized | at least one `Authority=` line, `codesign --verify --deep --strict` exits 0, `spctl` accepts with `source=Notarized Developer ID` | signed and notarized | **kept** (set, see below) | unaffected |
| signed, valid, not notarized | as above, but `spctl` rejects or names another source | signed, not notarized | cleared | unaffected |
| signed, invalid | at least one `Authority=` line and `codesign --verify --deep --strict` fails | invalid | nothing is installed | **held**, both policies |
| signed, certificate revoked | `spctl` output holds `CERT_REVOKED` | revoked | nothing is installed | **held**, both policies |

Two rules behind the table:

- **Ad hoc counts as not signed, whatever `codesign --verify` says about it.** Every arm64 binary carries at least a linker signature, and an unsigned Electron build is exactly that. Such a bundle names no one, and `--verify --deep --strict` may well fail on it (no sealed resources). Treating that as "an invalid signature that is present" would hold most apps on the list. "Present" therefore means: signed with a certificate.
- **Quarantine is kept only when macOS would open the app.** A valid Developer ID signature without notarization is rejected by Gatekeeper, so keeping the flag would install an app that does not start. Those apps are cleared as today, and the line says so.

### `internal/install`

`identity.go` (no build tag, so it tests on Linux):

```go
// Signature is what the code signature of a bundle amounts to.
type Signature string

const (
	Unsigned Signature = ""        // no signature at all
	AdHoc    Signature = "adhoc"   // signed without a certificate: names no one
	Valid    Signature = "valid"   // signed with a certificate, and codesign --verify --deep --strict passes
	Invalid  Signature = "invalid" // signed with a certificate, and verification fails
)

type Identity struct {
	BundleID string
	TeamID   string // as today: only when codesign confirms it
	// The rest is filled by Install only, not by Installer.Identity.
	Signature  Signature
	Authority  string // first "Authority=" line: "Developer ID Application: Jane Doe (ABCDE12345)"
	Problem    string // codesign's or spctl's last line when Invalid or Revoked
	Assessed   bool   // spctl gave an answer that can be used
	Notarized  bool   // spctl accepts with source=Notarized Developer ID
	Revoked    bool
	Source     string // spctl's "source=" value
	Stapled    bool   // a notarization ticket is stapled to the bundle
}

// KeepQuarantine reports whether the app goes into place with the quarantine
// flag: a valid signature and Apple's notarization.
func (i Identity) KeepQuarantine() bool { return i.Signature == Valid && i.Notarized && !i.Revoked }

// parseSignature reads `codesign -dv --verbose=4`: unsigned for "code object
// is not signed at all", ad hoc for "Signature=adhoc", else signed is true
// when there is an "Authority=" line, whose first value is returned.
func parseSignature(out string) (signed, adhoc bool, authority string)

// parseAssessment reads `spctl --assess --type execute -vv`: accepted is the
// word after "<path>: ", source the "source=" value. usable is false when the
// output holds "override=" (Gatekeeper is switched off) or neither word.
func parseAssessment(out string) (accepted bool, source string, revoked, usable bool)

// quarantineValue is the value of com.apple.quarantine for an app Lieferling
// downloaded at t: "<flags>;<seconds since 1970 in hex>;Lieferling;".
func quarantineValue(t time.Time) string
```

`install_darwin.go`:

```go
func (m *mac) Install(ctx context.Context, file, assetName string, inspect Inspect) (Result, error) {
	u, err := unpack(ctx, file, assetName)
	// …
	keep := false
	if inspect != nil {
		id := assess(ctx, u.source, identify(ctx, u.source))
		if err := inspect(ctx, Unpacked{Path: u.source, Name: u.name, Identity: id}); err != nil {
			return Result{}, err
		}
		keep = id.KeepQuarantine()
	}
	return m.place(ctx, u, keep)
}

// assess fills the signature and notarization fields of id.
func assess(ctx context.Context, app string, id Identity) Identity
```

`assess`, each command under its own 60 s deadline and with `CombinedOutput` (all three print to stderr):

1. `codesign -dv --verbose=4 <app>` and `parseSignature`. Unsigned or ad hoc: return.
2. `codesign --verify --deep --strict --verbose=2 <app>`. Exit 0: `Valid`. Otherwise `Invalid` with the last output line in `Problem`; return. (As built: `CERT_REVOKED` in the output counts as revoked and `CSSMERR_TP_NOT_TRUSTED` as `Untrusted`, which reads like ad hoc; a tool that is missing, timed out or gave no output leaves the signature `Unknown`, which holds nothing and clears the quarantine as before. Both mappings need hand check on a Mac.)
3. `spctl --assess --type execute -vv <app>` and `parseAssessment`. `Notarized = accepted && source == "Notarized Developer ID"`. Not usable, or the deadline passed: `Assessed = false`, and the app counts as not notarized.
4. Stapled: when `xcode-select -p` succeeds, `xcrun stapler validate <app>` exit 0. When it does not (no command line tools; calling `stapler` then makes macOS offer to install them), the existence of `<app>/Contents/CodeResources`, which is where a stapled ticket lives. `Stapled` is information for the line; the decision rests on `spctl`.

`identify` stays as it is and keeps serving `Installer.Identity` for the installed copy; it must not run `spctl` on every list refresh.

`place(ctx, u, keep bool)`: the script becomes

```
keep:   rm -rf NEXT && ditto SRC NEXT && xattr -rw com.apple.quarantine 'VALUE' NEXT && rm -rf TARGET && mv NEXT TARGET
clear:  rm -rf NEXT && ditto SRC NEXT && xattr -cr NEXT && rm -rf TARGET && mv NEXT TARGET     (today's line)
```

`Result` gets `Quarantined bool`. `install_other.go` needs no change beyond compiling.

#### Why Lieferling has to set the flag

macOS sets `com.apple.quarantine` only for files written by an app that opted in (`LSFileQuarantineEnabled` in its `Info.plist`) or that calls the quarantine API: browsers, Mail, AirDrop. Lieferling's `build/darwin/Info.plist` has no such key, a Go process writing with `os.CreateTemp` and `net/http` does not call the API, and a command line tool inherits nothing from Terminal. `hdiutil attach` and `ditto -x -k` only pass on a flag the archive already has. So a package Lieferling downloads carries no flag, today's `xattr -cr` removes nothing that matters for Gatekeeper, and "the quarantine stays" would be an empty promise: without the flag, Gatekeeper does not look at the app at first launch.

To get the first-launch check the issue wants, `place` writes the flag in the format macOS uses: `flags;hex timestamp;agent;UUID`, here `0081;<hex seconds>;Lieferling;` with an empty UUID (the UUID points into the LaunchServices download history, where Lieferling has no entry). `-r` writes it on every file of the bundle, as an unarchiver does. Homebrew Cask keeps quarantine on what it installs in the same spirit. The exact flags are the first hand check, because of app translocation; see "Hand checks" and Open point 1.

Policy `off` passes no inspect, so nothing is assessed and `place` clears as today.

### `internal/verify`

```go
const (
	CheckSignature    = "signature"
	CheckNotarization = "notarization"
)

// Signing is what the installer read from the bundle's code signature.
type Signing struct {
	State     string // "", "adhoc", "valid", "invalid" (install.Signature)
	Authority string
	Problem   string
	Assessed  bool
	Notarized bool
	Revoked   bool
	Stapled   bool
}

// SigningChecks gives the signature check and, unless the package is held by
// it, the notarization check.
//   signature:     skip "unsigned" | skip "adhoc" | pass "valid" (Got authority)
//                  | fail "invalid" (Want authority, Got problem) | fail "revoked" (Got authority)
//   notarization:  pass "notarized" | pass "stapled" (notarized, ticket attached)
//                  | skip "unnotarized" | skip "unsigned" | skip "unassessed"
func SigningChecks(s Signing) []Check
```

`Decide` needs no new rule: a failing `signature` is "any failing check other than sender", and passes of these two IDs do not count towards `Cleared` (delivery 2 already wrote the rule that way).

The store's inspect closure appends `verify.SigningChecks(...)` after the sender check, built from `u.Identity`. The sentences are written so they are true before and after the install, because the same report serves `lieferling verify` and a held note:

| ID | Result, code | English | German |
|---|---|---|---|
| `signature` | pass `valid` | Signed by {got}, and the signature is intact (codesign). | Signiert von {got}, und die Signatur ist unversehrt (codesign). |
| | fail `invalid` | The app carries a signature by {want}, but it is not valid: {got}. The app was changed after signing, or the signature is damaged. | Die App trägt eine Signatur von {want}, aber sie ist ungültig: {got}. Die App wurde nach dem Signieren verändert, oder die Signatur ist beschädigt. |
| | fail `revoked` | Apple has revoked the certificate of {got}, with which this app is signed. | Apple hat das Zertifikat von {got} widerrufen, mit dem diese App signiert ist. |
| | skip `unsigned` | The app is not signed. | Die App ist nicht signiert. |
| | skip `adhoc` | The app is signed ad hoc, without a certificate. That names no one. | Die App ist ad hoc signiert, ohne Zertifikat. Das nennt niemanden. |
| `notarization` | pass `notarized` | Apple has notarized this app (spctl). When installing, Lieferling marks it as downloaded (quarantine), so macOS checks it at first launch. | Apple hat diese App notarisiert (spctl). Beim Installieren kennzeichnet Lieferling sie als geladen (Quarantäne), damit macOS sie beim ersten Start prüft. |
| | pass `stapled` | Apple has notarized this app and the ticket is attached to it (spctl, stapler). When installing, Lieferling marks it as downloaded (quarantine), so macOS checks it at first launch. | Apple hat diese App notarisiert, und das Ticket hängt an (spctl, stapler). Beim Installieren kennzeichnet Lieferling sie als geladen (Quarantäne), damit macOS sie beim ersten Start prüft. |
| | skip `unnotarized` | Apple has not notarized this app. When installing, Lieferling removes the quarantine flag, because macOS would refuse to open it otherwise. | Apple hat diese App nicht notarisiert. Beim Installieren entfernt Lieferling die Quarantäne, weil macOS sie sonst nicht öffnen würde. |
| | skip `unsigned` | Not signed, so not notarized. When installing, Lieferling removes the quarantine flag so that macOS opens the app. | Nicht signiert, also nicht notarisiert. Beim Installieren entfernt Lieferling die Quarantäne, damit macOS die App öffnet. |
| | skip `unassessed` | macOS gave no assessment of this app (spctl), so Lieferling treats it as not notarized and removes the quarantine flag when installing. | macOS hat diese App nicht bewertet (spctl), Lieferling behandelt sie deshalb als nicht notarisiert und entfernt beim Installieren die Quarantäne. |

**Which lines may mention Apple:** only `signature` and `notarization`. `TestWording` changes from "no sentence mentions apple, notar or gatekeeper" to: no summary does, and no sentence of any other ID does. The two new IDs are also required to name the tool whose answer they report (`codesign`, `spctl`), so "Apple has notarized" is never Lieferling's own claim.

### The window

- A fourth line, last, named as in the mockup: **Apple-Signatur**. `linesOf` appends `signingLine(r, checks)` when the report has a `signature` check. It folds `signature` and `notarization` into one line, as the checksum line folds three checks.

  | Outcome | Icon | »Apple-Signatur« reads |
  |---|---|---|
  | `unsigned`, `adhoc`, installed | dots | nicht signiert, Quarantäne wurde entfernt (exactly the mockup) |
  | `valid` + `notarized` or `stapled`, installed | tick | signiert von `ABCDE12345`, von Apple notarisiert, Quarantäne bleibt |
  | `valid` + `unnotarized` or `unassessed`, installed | tick | signiert von `ABCDE12345`, nicht notarisiert, Quarantäne wurde entfernt |
  | `invalid` | alert | die Signatur von `ABCDE12345` ist ungültig |
  | `revoked` | alert | Apple hat das Zertifikat von `ABCDE12345` widerrufen |

  The team comes from `report.sender.teamId`; without one, the authority string. In a note for a package that was not installed (`held`, `ask`) the quarantine clause is left out, since nothing happened to any flag.
- **The tag does not count this line.** The mockup shows four lines and »3 VON 3«: the tag counts the comparisons Lieferling makes (Prüfsumme, Herkunft, Absender), and the Apple line reports what macOS says and what Lieferling did with the flag. `showTag` therefore counts over the lines without the signing line. A failing signing line still names the tag (»APPLE-SIGNATUR« in express red), like any line that stops a package.
- No fourth stop in the tracking strip.
- **Policy for all apps:** a submenu in the app menu, after »Kommandozeilen-Tool installieren …«:

  ```go
  zoll := m.AddSubmenu("Zollkontrolle")
  standard := zoll.AddRadio("Standard", true)
  strict := zoll.AddRadio("Streng", false)
  zoll.AddSeparator()
  zoll.Add("Was wird geprüft?").OnClick(/* opens the README chapter */)
  ```

  `OnClick` of a radio calls `svc.SetPolicy(ctx, "", "standard"|"strict")`. `AppService.remember` (which every list refresh passes through) reads `store.Policies().Global` and calls `SetChecked` on the matching item, so a change synced from another Mac or made on the command line shows in the menu. If `SetPolicy` fails, the old item is checked again.
- **Exception per app:** one entry in the row menu, after »Vorabversionen verfolgen«: `<button type="button" role="menuitem" data-act="policy">Zollkontrolle …</button>`, registered with `rowAction("policy", …)` in a new `features/policy.ts`. It opens a dialog through `ask`, built like the remove dialog:

  - title »Zollkontrolle für {Name}«
  - four radio buttons: »Wie alle Apps ({Standard|Streng})«, »Standard: prüfen, was sich prüfen lässt«, »Streng: Prüfsumme und Herkunftsnachweis sind Pflicht«, »Aus: diese App nicht prüfen«
  - under »Streng«, for an app that is not on GitHub: »{Forge} stellt keine Herkunftsnachweise aus. Streng hält jede Version dieser App an.«
  - buttons »Abbrechen« (focus) and »Übernehmen«

  It calls `AppService.SetPolicy(repo, value)` with `default`, `standard`, `strict` or `off`. The current value is `Status.policy`; the global one comes from a new `AppService.Policy() string`.
- The workflow pin gets no control in the window. It is a developer's setting, lives on the command line and in `config.json`, and shows in the note through the line »Herkunft«.
- `service.go`: `SetPolicy(ctx, repo, policy string) error` (translate, then `changed`) and `Policy() string`.
- Run `modern-web-guidance` and `impeccable` before the dialog, as `AGENTS.md` asks. No new tokens. `DESIGN.md`: the Apple line, the tag rule, the dialog.

### README chapter "Customs check" (delivery 3)

Replaces the bullet under "How it picks what to install" with a chapter:

1. What is compared, one paragraph per line of the note: checksum (three sources), origin (attestation: what it proves and what not), sender (trust on first use), Apple signature.
2. The verdicts as the window and `lieferling list` show them.
3. Policies: `standard`, `strict`, `off`; the menu, the row entry, `lieferling policy`; what `strict` means for Forgejo, Gitea and private GitHub repositories without attestations.
4. For developers: how to publish attestations (the four lines of workflow from this ADR), how users pin the workflow, and that Lieferling itself is attested.
5. Quarantine: which apps keep the flag and what macOS then asks at first launch.
6. `lieferling verify` in scripts, the exit codes, `--accept-sender`, `--accept-digest`.
7. Limits, plainly: attestations exist only on GitHub and for private repositories only on paid plans; a fine-grained token needs "Attestations: read"; the two TUF hosts Lieferling contacts; every Mac must run a current Lieferling for synced digests and pins to hold; nothing here says the code of an app is harmless.

Also fix the "Install" bullet, which says the quarantine flag is always cleared.

## Tests (`go test ./internal/... ./cmd/...`)

Delivery 2:

| Package | Test | What it pins |
|---|---|---|
| `verify` | `TestDecide` (extended) | attestation fail holds under standard; strict without attestation appends `attestation_required`; strict lacking both appends both lines; calling `Decide` twice appends nothing twice; a `signature` pass alone stays `unchecked` |
| | `TestNormalizeWorkflow` | `release.yml`, full path, leading slash, `none`, `.yaml`; rejects a path outside `.github/workflows`, an "@", a missing extension |
| | `TestRequireWorkflow` | appended on skip, not on pass, not on fail, not without a pin |
| | `TestOrigin` | `ShortRef` for branch, tag and other refs; `ShortCommit`; `WorkflowFile` |
| | `TestWording` (extended) | every new (ID, result, code) has both sentences; `{workflow}`, `{ref}`, `{commit}`, `{repo}` are filled; a `Signer` shows; the Apple rule per ID |
| `forge` | `TestAttestations` | 404 gives none; an empty array gives none; inline `bundle`; `bundle_url` fetched without the token and snappy-decoded; a 403 is an error; a non-GitHub repository gives `ErrNoAttestations`; a malformed digest never reaches the server |
| `attest` | `TestVerifyPublicBundle` | fixture bundle + fixture trust root, no network: verified, with repository, workflow, ref and commit |
| | `TestVerifyRejects` | wrong digest is `ErrInvalid`; another repository is `Mismatch{ErrRepository}`; repository in other case still passes; a pin on another workflow is `Mismatch{ErrWorkflow}`; a trust root that does not fit is `ErrInvalid`; a bundle that is not JSON is `ErrInvalid` |
| | `TestReusableWorkflow` | the reusable-workflow fixture verifies against the calling repository, `Workflow` is the caller's file and `Signer` names the reusable one |
| | `TestWorkflowOf` | branch ref, tag ref with "@", commit ref, repository in other case, a file name holding "@" |
| `config` | `TestMergeWorkflow` | filled when empty, the main file's kept on contradiction, survives `Update` |
| `store` | `TestAttestationPasses`, `TestAttestationFailsHolds` | with a fake `Attester` and a served `/attestations/` route: `Origin` in the report and in `state.json`; a failure holds before the installer is called |
| | `TestStrictNeedsAttestation` | no attestation: held with the policy line, the installer was never called; a Forgejo app under strict is held with `unsupported` plus the policy line |
| | `TestWorkflowPinIsMandatory` | pin + no attestation under standard: held with `workflow_required` |
| | `TestNoAttestationAfterDigestFailure` | a wrong forge digest: the attestation route is never requested |
| | `TestAcceptDigest` | contested entry: held; `InstallAccepting` with the right digest installs and replaces the entry; with another digest stays held; a forge digest mismatch is not overridable |
| | `TestVerifyWritesNothing` | `Verify` leaves `state.json` and `config.json` byte-identical, places nothing, returns `ErrAsk` for a sender change and `ErrHeld` under `Strict` |
| | `TestSetPolicy`, `TestSetWorkflow`, `TestPolicies` | validation, `default` clears, `off` only per app, pin refused off GitHub, `Unmet` |
| `cmd/lieferling` | `TestPolicyCommand`, `TestVerifyCommand`, `TestAcceptDigestFlag`, `TestReorderValueFlags` | the table, the warnings, exit code 3, `--json` is valid JSON and nothing else, `--workflow release.yml` in both spellings and positions |

Fixtures for `attest` (commit them with a `testdata/README` naming source and licence): `sigstore-js-2.1.0-bundle.json` and `reusable-workflow-attestation.sigstore.json` from `cli/cli` (MIT), `trusted-root-public-good.json` from sigstore-go's `examples/` (Apache-2.0). The scratch test for this ADR verified both bundles against that root offline. There is no offline fixture for GitHub's own instance; that path is a hand check.

Delivery 3:

| Package | Test | What it pins |
|---|---|---|
| `install` | `TestParseSignature` | recorded `codesign -dv --verbose=4` output of a Developer ID app, an ad hoc app, a linker-signed Electron build, an unsigned app |
| | `TestParseAssessment` | recorded `spctl` output: notarized, unnotarized Developer ID, no usable signature, revoked, Gatekeeper switched off |
| | `TestKeepQuarantine` | the table under "What counts as what" |
| | `TestQuarantineValue` | format and hex timestamp |
| `verify` | `TestSigningChecks` | every row; no notarization check after a failed signature |
| | `TestWording` (extended) | the new sentences; only these two IDs may mention Apple; each names `codesign` or `spctl` where it reports their answer |
| `store` | `TestInvalidSignatureHolds` | fake installer with `Signature: Invalid`: held under standard, nothing placed |
| | `TestSigningInReport` | the checks arrive in order after the sender; a notarized app reports `notarized`; verdict of an unsigned first install stays `unchecked` |
| | `TestSetPolicyFromService` (root package is not tested; cover `store.SetPolicy` and `Policies`) | as delivery 2 |

The recorded outputs come from the hand checks below; until they exist, write the fixtures from the shapes in this ADR and replace them.

## Order of work

**Delivery 2** (about five days), branch from `main`, PR `feat: verify build attestations, add policy and verify commands (#30, Lieferung 2)`:

1. `go get github.com/sigstore/sigstore-go@v1.4.0`, Go 1.27 in `go.mod`; confirm CI and the release Taskfile build. `internal/attest` with fixtures and tests. (Day 1 to 2)
2. `verify`: `Origin`, `Attested`, `NotAttested`, `RequireWorkflow`, `SyncedAccepted`, `NormalizeWorkflow`, `Decide`, wording, tests. (Day 2)
3. `forge.Attestations` with tests. `config.App.Workflow` and its merge rule. (Day 2 to 3)
4. `store`: `Attester`, `attestationCheck`, the early `Decide`, `Accept`, `Verify`, `SetPolicy`, `SetWorkflow`, `Policies`, `Status.Policy`, tests. (Day 3 to 4)
5. Command line: `policy`, `verify`, `--accept-digest`, `reorder`, help, tests. (Day 4)
6. Window: the line »Herkunft«, bindings, `DESIGN.md`. README additions. (Day 5)
7. `ci: attest release files` as the last commit. Hand checks 1 to 6. (Day 5)

**Delivery 3** (about four days), branch from the delivery 2 branch, PR `feat: read signature and notarization, keep quarantine for notarized apps (#30, Lieferung 3)`:

1. On a Mac first: hand checks 7 to 11, record the tool outputs as fixtures. They decide the quarantine flags and whether Open point 1 falls back. (Day 1)
2. `install`: `Signature`, the parsers, `assess`, `place(keep)`, `quarantineValue`, tests. (Day 1 to 2)
3. `verify.SigningChecks`, wording, `TestWording`; the store's inspect closure. (Day 2)
4. Window: the line »Apple-Signatur«, the tag rule, `features/policy.ts` and its dialog, the menu in `main.go`, `service.go`. (Day 3)
5. README chapter, `DESIGN.md`, the remaining hand checks. (Day 4)

If time runs short, cut in this order. Delivery 2: `--json`, then the `Unmet` warnings. Delivery 3: `Stapled` (and the `stapled` code), then the »Was wird geprüft?« menu item. Do not cut the fixtures, `TestWording`, or the rule that ad hoc is not "invalid".

Each PR publishes a minor release when merged. Delivery 3 is rebased onto `main` after delivery 2 is merged.

## Hand checks on a Mac

Nothing below can be run from the Linux CI. Each is marked with the delivery it gates.

Delivery 2:

1. **TUF from a real network:** first `lieferling verify pklaschka/lieferling` after the first attested release creates `~/Library/Caches/Lieferling/sigstore` and passes; a second run within a day makes no request to `tuf-repo-cdn.sigstore.dev` (check with Little Snitch or `nettop`).
2. **Offline trust root:** block `tuf-repo-cdn.sigstore.dev` with an empty cache: the line reads `trust`, `standard` installs, `strict` holds.
3. **A public repository with attestations** (`cli/cli`): `curl https://api.github.com/repos/cli/cli/attestations/sha256:<digest of a release asset>` shows whether the answer carries `bundle`, `bundle_url` or both. Then `lieferling verify cli/cli` must show `verified`. (The `cli/cli` release is a `.zip` or `.pkg` without an `.app`; use any attested app instead if `pick.Asset` finds nothing.)
4. **A private repository with attestations**, if one is at hand (needs GitHub Enterprise Cloud): this is the only test of GitHub's own Sigstore instance, of `tuf-repo.github.com` and of the embedded `github-root.json`. Without one, say so in the PR.
5. **A fine-grained token without "Attestations: read"** on a private repository: the line reads `unreadable`, not a crash and not a hold under `standard`.
6. **Lieferling updating itself under `strict` with the pin**, from the first attested release to the next one.

Delivery 3:

7. **Record the tools.** For one app of each kind (Developer ID and notarized; Developer ID, not notarized; ad hoc or linker-signed Electron build; unsigned Intel build; a notarized app with one file changed after signing): the output and exit code of `codesign -dv --verbose=4`, `codesign --verify --deep --strict --verbose=2`, `spctl --assess --type execute -vv`, `xcrun stapler validate`. These become the fixtures. In particular: what `--verify --deep --strict` says about an unsigned arm64 Electron app, which is the case the ad hoc rule exists for.
8. **Does a download carry the flag?** `xattr -l` on the temp file and on the app in the mounted DMG during an install from the app and from the command line. Expected: no `com.apple.quarantine`.
9. **The flag Lieferling writes.** Install a notarized app with `0081;<ts>;Lieferling;`. Then: does first launch show Gatekeeper's dialog, and what does it say about where the app came from? Is the app translocated (its running path under `/private/var/folders/…/AppTranslocation/`)? Compare with `xattr -p com.apple.quarantine` of the same app unzipped from a Safari download, before and after its first launch, and of an app installed by Homebrew Cask. If the app is translocated, repeat with the flags those two show (probably `0181`).
10. **Reopen after update:** update a running notarized app; `place` reopens it at once, so the dialog appears right after the install. Confirm it does not hang the install.
11. **An invalid signature holds:** the tampered app from check 7 as a release asset: held, the running copy keeps running, the note names »Apple-Signatur«.
12. **Gatekeeper switched off** (`spctl --status` disabled): the line reads `unassessed`, the flag is cleared.
13. **A Mac without the command line tools:** no "install developer tools" dialog appears during an install.
14. **The menu and the dialog:** radio state after a change on another Mac; the dialog with the keyboard only and with VoiceOver; the note with four lines in light and dark and at 520 px width against the mockup.

Plus, for both: `GOOS=darwin GOARCH=arm64 go build ./cmd/lieferling`, `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go vet -tags purego .`, regenerated bindings, `npm run build`.

## Consequences

- The binaries grow by about 10 MB each and the module tree from 2 to 68 compiled modules (command line). Dependency updates for the Sigstore tree become a regular chore.
- Go 1.27 becomes the minimum.
- Lieferling contacts two new hosts, at most once a day: `tuf-repo-cdn.sigstore.dev`, and `tuf-repo.github.com` only for private repositories with attestations. Each install on GitHub costs one more API request.
- `strict` becomes much stricter with delivery 2: an app on `strict` whose releases carry no attestation is held from that release on. The release notes say so, and `lieferling policy` lists the apps that cannot meet it.
- A pin set while one Mac still runs a delivery 1 build can be stripped by that Mac.
- A notarized app shows Gatekeeper's first-launch dialog after every update. That is the check the issue asked for; whether it is bearable in daily use is Open point 1.
- An install of a signed app takes a few seconds longer (`codesign --verify --deep` hashes the whole bundle, `spctl` may ask Apple).
- An attestation proves where a file was built. A compromised workflow in the right repository still passes; the sentence says "where", never "safe".
- The embedded TUF roots age. sigstore-go ships its own with each release; `github-root.json` is copied from `cli/cli` and should be refreshed when sigstore-go is updated.

## Open points

1. **Setting the quarantine flag ourselves versus only reporting `spctl`'s verdict.** *As built in delivery 3:* both paths exist; `install.Options.MarkNotarized` chooses, and it is off until the hand checks are done, so a notarized app keeps the attributes it came with (none) and the note says so. The issue wants Gatekeeper to check at first launch, which needs the flag. The alternative is to set nothing, leave a notarized app's attributes alone, and report the assessment Lieferling already ran (`spctl` asks the same system policy Gatekeeper uses). Recommendation: build the flag as specified, and decide after hand checks 9 and 10. Fall back to the alternative if the app is translocated with every flag value tried (translocation breaks apps that update themselves), or if a dialog after every update is more than Zuri wants. The fallback changes one line of `place` and two sentences.
2. **Lieferling's own entry on `strict` by default.** Recommendation: not in code. Document it in the README and set it by hand after two or three attested releases went through cleanly; a built-in `strict` that meets a bug in the attestation path would lock self-update.
3. **`unchecked` and exit code 0 for `lieferling verify`.** Recommendation: keep 0, with `--strict` for scripts that want more.
4. **`--json` for `verify`.** Recommendation: include; it is the report as it already is. First to cut.
5. **A control for the workflow pin in the window.** Recommendation: no. Revisit if someone asks; the place would be the row dialog.
6. **Pinning through a catalog or a `lieferling://` link**, so a developer can hand users the pin with the app. Recommendation: its own issue after #30; it touches the catalog format.
7. **GitHub's release attestations** (predicate `https://in-toto.io/attestation/release/v0.1`, issued by GitHub itself for immutable releases). They say "this file belongs to this release", not "this workflow built it". Recommendation: not in #30; the forge digest already covers most of it. Requesting only `predicate_type=https://slsa.dev/provenance/v1` keeps them out.
8. **Self-hosted runners.** `gh` can refuse them (`RunnerEnvironment`). Recommendation: accept them; a developer's own Mac mini as a runner is common for macOS builds.
9. **sigstore-go v1.4.0 and Go 1.27, or v1.3.0 and Go 1.25.8.** Recommendation: v1.4.0. Fall back only if the release build breaks.

## What could not be verified here

- The live shape of the attestations answer for a repository that has attestations (the proxy allows only this repository). Taken from the GitHub CLI's client and tests; hand check 3 settles `bundle` versus `bundle_url`.
- Fetching either TUF repository: `tuf-repo-cdn.sigstore.dev` and `tuf-repo.github.com` are not reachable from this session. The options are read from sigstore-go and the GitHub CLI; hand checks 1, 2 and 4 cover them.
- GitHub's own Sigstore instance end to end: no fixture trust root exists offline (the GitHub CLI's `test/data/trusted_root.json` is the public one).
- The token permission for private repositories and the rate limit class for anonymous calls: `docs.github.com` is not reachable; the probe here was authenticated by the proxy and counted against `core`.
- The current major version of `actions/attest-build-provenance`.
- Everything that runs on a Mac: the exact output of `codesign`, `spctl` and `stapler`, what `codesign --verify --deep --strict` says about a linker-signed Electron bundle, the meaning of the quarantine flag bits, translocation, and Gatekeeper's behaviour with a flag Lieferling writes. The statements about them in this ADR are from knowledge of macOS, not from a run, and the hand checks are ordered so that the uncertain ones come before the code that depends on them.
- That Forgejo and Gitea have no attestation API was not probed (`codeberg.org` is not reachable); it rests on the issue and on the absence of any such call or field in the Gitea release API as `internal/forge/gitea.go` uses it.
