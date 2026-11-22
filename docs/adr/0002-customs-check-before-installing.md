# ADR 0002: Customs check before installing (#30), delivery 1 of 3

- **Status:** Proposed
- **Continued in:** [ADR 0003](0003-attestation-signature-and-quarantine.md), which specifies deliveries 2 and 3 and corrects two points of this one (the config fields, the open point on `--accept-digest`)
- **Date:** 2026-11-21
- **Decider:** Zuri

## Context

Lieferling installs whatever the newest release holds: `store.installRelease` downloads the asset, `mac.Install` unpacks it, quits the running app, replaces the copy in `/Applications` and clears the quarantine flag. Nothing checks that the file is the one the release lists, or that the app inside is the same app from the same sender as last time. Most apps on the list are unsigned and many are private, so Gatekeeper is no backstop either.

Issue #30 ("Zollkontrolle") adds a check between download and install. Binding decisions from the issue:

- Checksum, CI provenance attestation, and "same app, same sender as last time" are all in scope. Attestation is not a later stage.
- One global policy with per-app exceptions: `standard` (default: check what is available; a sender change asks), `strict` (checksum and attestation mandatory; a sender change holds), `off` (only as a per-app exception).
- Digests go into the synced list (`config.json` in iCloud), so each Mac compares with what another Mac saw for the same version. Config fields: `Config.Verify`, `App.Verify`, `App.BundleID`, `App.TeamID`, `App.Digests` (`"version/assetName"` to `"sha256:…"`, last few versions per app).
- A package that fails is not installed, its file is discarded, and the running app is only quit once the package passed. Self-update never installs a held package; it notifies.
- Merging iCloud conflict copies must union digests and hold on contradiction rather than overwrite.
- The result is shown as a "Zollvermerk" under the parcel. "Geprüft" must not sound like "checked by Apple"; every line says exactly what was checked.

The owner wants a stack of three PRs, one per delivery:

1. Checksum (forge digest, checksum file, computed while downloading), digest and sender in the synced list, remember and compare the sender, confirmation prompt.
2. Attestation with sigstore-go, policy global and per app, `lieferling verify <repo>` with an exit code.
3. Read signature and notarization, keep quarantine for notarized apps, full Zollvermerk UI, README.

ADR 0001 already opened the seams: `store.download` takes an `io.Writer`, `mac.Install` is `unpack` + `place`, rows take lines through `rowSection`, dialogs go through `ask`.

This ADR decides the overall shape for all three deliveries and specifies delivery 1 in full.

## Decision

1. **A new leaf package `internal/verify`** owns everything that can be decided without touching the system: policy, the check list, verdicts, digest and checksum-file parsing, sender comparison, and the wording of every check in English and German. It imports only the standard library. `internal/store` orchestrates, `internal/install` reads the bundle's identity, `internal/config` and `internal/state` store.
2. **The result is a `verify.Report`: a verdict plus a list of checks, each `pass`, `fail` or `skip` (not checkable) with one sentence.** Deliveries 2 and 3 add checks to the list; the store flow, the binding and the UI do not change shape.
3. **A held package is state, not an error dialog.** The report is written to `state.json` and comes back on `Status.Customs` with every list. The window never blocks an install waiting for an answer.
4. **"Ask" is two calls, not a blocking one.** The first install ends with verdict `ask` and discards the file. The user's yes is a second call, `AcceptSender`, which downloads again and accepts only the exact package (digest) and sender (bundle ID, team ID) that were shown. The app and the command line use the same store method.
5. **Delivery 1 includes a minimal Zollvermerk and the command line prompt.** The sender confirmation needs UI anyway, and a check whose outcome is invisible would look like a random install failure. Delivery 1 ships: the collapsed line, the expanded check list, the held and ask states, the confirmation dialog, the CLI prompt and `--accept-sender`. Delivery 3 keeps: signature and notarization lines, quarantine, visual polish, policy controls in the app, the README chapter.
6. **`Installer.Install` gets an inspect callback** that runs between `unpack` and `place` (as ADR 0001 planned). It is the only way to `place`, so "nothing is quit or replaced before the check passed" holds by construction, for self-update too.

### Options considered for the "ask" round-trip

| | A: Block inside the install, answer by event | B: Verdict `ask` as state, second call accepts (chosen) | C: Ask before downloading |
|---|---|---|---|
| Store lock while the dialog is open | held: every other install waits | free | free |
| DMG mounted while waiting | yes, for as long as the user thinks | no | no |
| "Alle aktualisieren" | stops at the first question | goes on, questions wait in their rows | n/a |
| App quit or crash while asking | leaks mount and temp files | nothing to clean | n/a |
| App and CLI share the path | no (event vs. prompt) | yes | n/a |
| Cost | none | a second download after a yes | impossible: the sender is only known after unpacking |

B costs a second download in a rare case (a sender changes when a developer starts signing, changes certificates or renames the bundle). The second download is bound to the digest the user saw, so a file swapped in between is not accepted. If the second download ever hurts, keeping the first file until the answer is an optimisation inside `store` that changes no interface.

## Delivery 1 in detail

### Flow

```
store.install (holds s.mu)
  newest                         pick release and asset (unchanged)
  download                       to a temp file, SHA-256 computed while streaming
  policy = verify.Resolve(cfg.Verify, app.Verify)        off: skip to Installer.Install with a nil inspect
  checks before unpacking:
    digest.forge                 the asset's digest field from the forge
    digest.checksums             a checksum file in the same release
    digest.synced                what the synced list holds for version/asset
  any fail -> verdict held: write the report to state.json, discard the file, return ErrHeld
  Installer.Install(file, name, inspect)
    unpack
    inspect(Unpacked):           runs in store
      sender                     identity in the package vs. synced list, else vs. installed copy
      verify.Decide              cleared | unchecked | ask | held
      ask or held -> return verify.ErrAsk / verify.ErrHeld: nothing was quit, nothing replaced
    place                        quit, replace, reopen (unchanged in delivery 1, still clears quarantine)
  record                         state.json: installation + report; config.json: app name, sender, digest
```

### `internal/verify` (new)

Files: `verify.go` (types, `Decide`), `policy.go`, `digest.go`, `checksums.go`, `sender.go`, `text.go` (wording), and a `_test.go` for each.

```go
package verify

// Policy is how strict the customs check is.
type Policy string

const (
	Standard Policy = "standard" // check what is available; a sender change asks
	Strict   Policy = "strict"   // a published checksum is mandatory; a sender change holds
	Off      Policy = "off"      // no check; valid only as a per-app exception
)

// Resolve gives the policy for one app. app (config.App.Verify) wins when set.
// global (config.Config.Verify) is used otherwise; "" and "off" as global mean
// Standard, because off exists only per app. Any unknown value means Strict:
// a typo or a level from a newer version must not weaken the check.
func Resolve(global, app string) Policy

// Stricter returns the stricter of two config values, in the order
// off < standard < "" < strict; unknown counts as strict. "" ranks above
// standard because an app without an exception follows the global policy,
// which is standard or strict. Used when merging conflict copies.
func Stricter(a, b string) string

type Result string

const (
	Pass Result = "pass"
	Fail Result = "fail"
	Skip Result = "skip" // not checkable: nothing to compare with
)

// Check IDs. Delivery 2 adds "attestation", delivery 3 "signature" and "notarization".
const (
	CheckForgeDigest = "digest.forge"
	CheckChecksums   = "digest.checksums"
	CheckSynced      = "digest.synced"
	CheckSender      = "sender"
	CheckPolicy      = "policy" // a requirement of strict that nothing met
)

// Check is one line of the report. ID, Result and Code are stable; Text is
// filled by Localize and never stored.
type Check struct {
	ID     string `json:"id"`
	Result Result `json:"result"`
	Code   string `json:"code"`             // why, see the table under "Wording"
	Source string `json:"source,omitempty"` // "GitHub", "codeberg.org", "SHA256SUMS"
	Want   string `json:"want,omitempty"`   // what was expected (digest, or sender as Sender.String())
	Got    string `json:"got,omitempty"`    // what was found
	Text   string `json:"text"`             // one sentence, see Localize
}

type Verdict string

const (
	Cleared   Verdict = "cleared"   // at least one check passed, none failed
	Unchecked Verdict = "unchecked" // nothing failed, but there was nothing to compare with
	Ask       Verdict = "ask"       // only the sender changed, policy standard: the user decides
	Held      Verdict = "held"      // not installed
	Disputed  Verdict = "disputed"  // installed, but another Mac saw a different package for this version
	Exempt    Verdict = "exempt"    // policy off for this app
)

// Sender is who a package claims to come from.
type Sender struct {
	BundleID string `json:"bundleId"`
	TeamID   string `json:"teamId,omitempty"` // "" for unsigned and ad-hoc signed apps
}

func (s Sender) IsZero() bool
func (s Sender) Equal(o Sender) bool // BundleID without case, TeamID exactly
func (s Sender) String() string      // "com.example.app (team ABCDE12345)" or "com.example.app (no team ID)"

// Report is the customs note for one package.
type Report struct {
	Version   string    `json:"version"`
	Asset     string    `json:"asset"`
	Digest    string    `json:"digest"` // "sha256:<64 lowercase hex>", computed while downloading
	Policy    Policy    `json:"policy"`
	Verdict   Verdict   `json:"verdict"`
	Checks    []Check   `json:"checks"`
	Before    *Sender   `json:"before,omitempty"` // set when the sender was compared and differs
	After     *Sender   `json:"after,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	Summary   string    `json:"summary"` // the collapsed line, filled by Localize, never stored
}

func (r Report) Failed() bool // any check with Result Fail

// Decide turns checks into a verdict. It may append a CheckPolicy line.
//
//   any digest.* fail                         -> Held
//   strict and neither digest.forge nor
//     digest.checksums passed                 -> append {policy, fail, "checksum_required"}, Held
//   sender fail, standard                     -> Ask
//   sender fail, strict                       -> Held
//   otherwise: at least one pass              -> Cleared, else Unchecked
func Decide(p Policy, checks []Check) (Verdict, []Check)

var (
	ErrHeld = errors.New("held at customs")
	ErrAsk  = errors.New("the sender changed and needs your confirmation")
)
```

`digest.go`:

```go
// FormatDigest is "sha256:" + lowercase hex of sum.
func FormatDigest(sum []byte) string

// Key is the key of config.App.Digests: version + "/" + asset. SplitKey cuts at
// the last "/", because a tag can hold a slash and an asset name cannot.
func Key(version, asset string) string
func SplitKey(key string) (version, asset string)

// Seen splits a synced value into its digests. One digest is the normal case;
// more than one means the Macs disagree (see "Merge rules").
func Seen(value string) []string
// Merge is the union of two synced values, sorted, joined by one space.
func Merge(a, b string) string

// ForgeDigest compares the digest the forge lists for the asset with got.
//   listed == ""                -> skip "none"
//   listed not "sha256:<64hex>" -> skip "algorithm"
//   equal (hex without case)    -> pass "match"
//   else                        -> fail "mismatch" (Want listed, Got got)
func ForgeDigest(forgeName, listed, got string) Check

// SyncedDigest compares what the synced list holds for this version and asset.
//   seen == ""                       -> skip "first"
//   more than one digest in seen     -> fail "contested"
//   equal                            -> pass "match"
//   else                             -> fail "mismatch"
func SyncedDigest(seen, got string) Check
```

`checksums.go`:

```go
// ChecksumFiles returns, in the order to try them, the names among assetNames
// that can hold a checksum for asset. All comparisons ignore case.
//   1. <asset>.sha256, <asset>.sha256sum          (per-file, may be a bare digest)
//   2. SHA256SUMS, SHA256SUMS.txt
//   3. any name ending in "checksums.txt"         (checksums.txt, app_1.2.0_checksums.txt)
func ChecksumFiles(assetNames []string, asset string) []string

// IsBare reports whether name is a per-file checksum (rule 1 above).
func IsBare(name, asset string) bool

// ParseChecksums finds asset's SHA-256 in a checksum file.
// Per line, after trimming; empty lines and lines starting with "#" are skipped:
//   GNU   "<hex>  <name>" or "<hex> *<name>"   name compared by its base name ("./dist/x.dmg" matches "x.dmg")
//   BSD   "SHA256 (<name>) = <hex>"
//   bare  "<hex>" alone, only when bare is true, and only if no named line matched
// Only 64-digit hex counts; other lengths (SHA-512 lines in a mixed file) are
// skipped. A file that names asset twice with different digests is an error.
// found is false when the file does not name asset.
func ParseChecksums(data []byte, asset string, bare bool) (digest string, found bool, err error)

// MaxChecksumFile is the most Lieferling reads of a checksum file: 1 MiB.
const MaxChecksumFile = 1 << 20

// ChecksumCheck is the check for one parsed file.
//   err != nil  -> fail "ambiguous"
//   !found      -> skip "unlisted"
//   equal       -> pass "match"
//   else        -> fail "mismatch"
func ChecksumCheck(file, digest string, found bool, err error, got string) Check
```

`sender.go`:

```go
// SenderCheck compares the sender in the package with the one known so far.
//   now.BundleID == "" and known.IsZero() -> skip "unreadable"
//   known.IsZero()                        -> skip "first" (Got now)
//   known.Equal(now)                      -> pass "same" (Got now)
//   accepted                              -> pass "accepted" (Want known, Got now)
//   else                                  -> fail "changed" (Want known, Got now)
func SenderCheck(known, now Sender, accepted bool) Check
```

#### Wording

`text.go` holds one table with both languages and fills `Check.Text` and `Report.Summary`:

```go
type Lang int
const (English Lang = iota; German)

// Localize fills Summary and every Check.Text in lang. Placeholders:
// {source}, {want}, {got}, {version}.
func Localize(r *Report, lang Lang)
```

The German sentences live next to the codes in `internal/verify`, not in `translate.go`. That bends "no UI code in `internal/`", on purpose: the root package is not covered by `go test ./internal/... ./cmd/...`, and the rule "every line says exactly what was checked" is only enforceable if a test sees every sentence. `translate.go` keeps doing substring replacement for free-form errors.

Collapsed line (`Report.Summary`; the window prints "Zoll" as the label in front, the CLI prints "Customs: "):

| Verdict | German | English |
|---|---|---|
| `cleared` | Geprüft und freigegeben | checked and cleared |
| `unchecked` | Durchgelassen, es gab nichts zu vergleichen | let through, there was nothing to compare with |
| `ask` | {version} angehalten: Der Absender hat sich geändert | {version} held: the sender changed |
| `held` | {version} angehalten und nicht installiert | {version} held and not installed |
| `disputed` | Widerspruch: Ein anderer Mac hat für diese Version ein anderes Paket gesehen | disputed: another Mac saw a different package for this version |
| `exempt` | Nicht geprüft, für diese App abgeschaltet | not checked, switched off for this app |

`unchecked` is new against the issue, which names only "Geprüft und freigegeben". A Forgejo release without a checksum file, installed for the first time, was compared with nothing; calling that "geprüft" is exactly the overclaim the issue warns about.

Check lines (`{source}` is `forge.Repo.Forge()` for `digest.forge` and the file name for `digest.checksums`; senders are written with `Sender.String()`, in German "com.example.app (Team-ID ABCDE12345)" and "com.example.app (ohne Team-ID)"):

| ID | Result, code | German | English |
|---|---|---|---|
| `digest.forge` | pass `match` | Die SHA-256-Prüfsumme der geladenen Datei stimmt mit der überein, die {source} für diese Datei führt. | The SHA-256 of the downloaded file matches the one {source} lists for this file. |
| | fail `mismatch` | Die Prüfsumme der geladenen Datei weicht von der ab, die {source} für diese Datei führt. Der Download ist beschädigt oder wurde unterwegs verändert. | The SHA-256 of the downloaded file differs from the one {source} lists for this file. The download is damaged or was changed on the way. |
| | skip `none` | {source} führt für diese Datei keine Prüfsumme. | {source} lists no checksum for this file. |
| | skip `algorithm` | {source} führt eine Prüfsumme in einem Verfahren, das Lieferling nicht kennt. | {source} lists a checksum in a format Lieferling does not know. |
| `digest.checksums` | pass `match` | Die Prüfsumme stimmt mit dem Eintrag in {source} aus demselben Release überein. Das zeigt, dass der Download unversehrt ist, nicht, wer ihn gebaut hat. | The checksum matches the entry in {source} from the same release. This shows the download is intact, not who built it. |
| | fail `mismatch` | Die Prüfsumme weicht vom Eintrag in {source} aus demselben Release ab. | The checksum differs from the entry in {source} from the same release. |
| | fail `ambiguous` | {source} nennt diese Datei mehrfach mit verschiedenen Prüfsummen. | {source} names this file more than once with different checksums. |
| | skip `none` | Das Release enthält keine Prüfsummendatei. | The release has no checksum file. |
| | skip `unlisted` | {source} nennt diese Datei nicht. | {source} does not name this file. |
| | skip `unreadable` | {source} ließ sich nicht laden. | {source} could not be loaded. |
| `digest.synced` | pass `match` | Dasselbe Paket, das deine Macs für diese Version schon gesehen haben. | The same package your Macs have seen for this version before. |
| | fail `mismatch` | Für dieselbe Version hat einer deiner Macs ein anderes Paket gesehen. Das Release wurde nachträglich ausgetauscht. | One of your Macs saw a different package for the same version. The release was replaced after it was published. |
| | fail `contested` | Deine Macs haben für diese Version verschiedene Pakete gesehen. | Your Macs saw different packages for this version. |
| | skip `first` | Diese Version hat noch keiner deiner Macs installiert, es gibt nichts zu vergleichen. | None of your Macs has installed this version yet, there is nothing to compare with. |
| `sender` | pass `same` | Derselbe Absender wie bisher: {got}. | The same sender as before: {got}. |
| | pass `accepted` | Der Absender hat sich geändert, und du hast ihn angenommen: bisher {want}, jetzt {got}. | The sender changed and you accepted it: was {want}, now {got}. |
| | fail `changed` | Der Absender hat sich geändert: bisher {want}, jetzt {got}. | The sender changed: was {want}, now {got}. |
| | skip `first` | Erste Lieferung dieser App. Der Absender {got} gilt ab jetzt als bekannt. | First delivery of this app. The sender {got} counts as known from now on. |
| | skip `unreadable` | Das Paket nennt keine Bundle-ID, der Absender lässt sich nicht vergleichen. | The package names no bundle ID, the sender cannot be compared. |
| `policy` | fail `checksum_required` | Die strenge Kontrolle verlangt eine veröffentlichte Prüfsumme, das Release hat keine. | The strict policy requires a published checksum and the release has none. |

### `internal/forge`

- `Asset` gets `Digest string \`json:"digest"\`` with the comment "sha256:… as GitHub lists it for assets uploaded since mid-2025; empty on older assets and on Forgejo and Gitea". GitHub's `null` decodes to `""`. `giteaRelease` stays as it is (no such field); `giteaRelease.release()` leaves `Digest` empty.
- Nothing else changes. Checksum files are ordinary assets and are read with the existing `Client.Download`, so tokens and the "token only to the repository's own host" rule apply unchanged.

### `internal/install`

```go
// Identity is what an app bundle says about itself.
type Identity struct {
	BundleID string // CFBundleIdentifier
	TeamID   string // Developer ID team, only when codesign confirms it; else ""
}

// Unpacked is an app taken out of its download, before anything is replaced.
type Unpacked struct {
	Path     string // the .app in the mounted DMG or the unzipped folder
	Name     string // bundle name without .app
	Identity Identity
}

// Inspect looks at an unpacked app. An error stops the install before the
// running app is quit or anything is replaced.
type Inspect func(ctx context.Context, u Unpacked) error

type Installer interface {
	// Install installs the downloaded file. inspect, when not nil, runs after
	// unpacking and before replacing.
	Install(ctx context.Context, file, assetName string, inspect Inspect) (Result, error)
	// Identity reads the identity of the app installed at path; the zero value
	// when there is none or it cannot be read.
	Identity(ctx context.Context, path string) Identity
	Uninstall(...)        // unchanged
	InstalledVersion(...) // unchanged
	Open(...)             // unchanged
}
```

`install_darwin.go`:

```go
func (m *mac) Install(ctx context.Context, file, assetName string, inspect Inspect) (Result, error) {
	u, err := unpack(ctx, file, assetName)
	if err != nil {
		return Result{}, err
	}
	defer u.close()
	if inspect != nil {
		if err := inspect(ctx, Unpacked{Path: u.source, Name: u.name, Identity: identify(ctx, u.source)}); err != nil {
			return Result{}, err
		}
	}
	return m.place(ctx, u)
}

func (m *mac) Identity(ctx context.Context, path string) Identity { return identify(ctx, path) }

// identify reads the bundle ID with plutil and the team with codesign.
func identify(ctx context.Context, app string) Identity
```

`identify`:

1. `plutil -extract CFBundleIdentifier raw <app>/Contents/Info.plist`, trimmed. On error: `""`.
2. `codesign -dv --verbose=2 <app>` (it prints to stderr; use `CombinedOutput`) and `parseTeamID`.
3. If a team came out: `codesign --verify --strict -R=<requirement> <app>` with the requirement `anchor apple generic and certificate leaf[subject.OU] = "<team>"`. If that fails the team is `""`. A team ID is otherwise just a string inside a signature blob that anyone can write; with this call the sender comparison rests on Apple's certificate chain. The call names what it checks ("signed by this team") and nothing more; whether the whole bundle is intact, notarized and not revoked is delivery 3.

A new untagged file `identity.go` holds the part that tests on Linux:

```go
// parseTeamID finds "TeamIdentifier=ABCDE12345" in codesign's output. "not set"
// and anything that is not ten characters of A-Z and 0-9 give "".
func parseTeamID(codesignOutput string) string
```

The ten-character check also makes the team safe to put into the requirement string.

`install_other.go`: `unsupported` gets the new `Install` signature and `Identity` returning the zero value.

`place` is not touched in delivery 1 (it still runs `xattr -cr`).

### `internal/config`

```go
type App struct {
	Repo    string `json:"repo"`
	Channel string `json:"channel"`
	AppName string `json:"appName,omitempty"`
	// Verify is this app's exception from Config.Verify: "", "standard", "strict" or "off".
	Verify string `json:"verify,omitempty"`
	// BundleID and TeamID are the sender of the last package installed on any
	// computer. TeamID is "" for unsigned apps.
	BundleID string `json:"bundleId,omitempty"`
	TeamID   string `json:"teamId,omitempty"`
	// Digests maps "version/assetName" to "sha256:…" for the last few versions,
	// so every computer compares with what another one saw. A value with two
	// digests separated by a space means the computers disagree.
	Digests map[string]string `json:"digests,omitempty"`
}

type Config struct {
	Version int   `json:"version"` // stays 1: every new field is optional
	Apps    []App `json:"apps"`
	CheckIntervalHours int `json:"checkIntervalHours,omitempty"`
	// Verify is the customs policy for all apps: "" or "standard", or "strict".
	Verify string `json:"verify,omitempty"`
}
```

Example:

```json
{
  "version": 1,
  "verify": "standard",
  "apps": [
    {
      "repo": "pklaschka/feldspur",
      "channel": "stable",
      "appName": "Feldspur",
      "bundleId": "de.pklaschka.feldspur",
      "teamId": "ABCDE12345",
      "digests": {
        "1.4.0/Feldspur-1.4.0-arm64.dmg": "sha256:9f2c…",
        "1.4.0/Feldspur-1.4.0-x64.dmg": "sha256:51aa…"
      }
    }
  ]
}
```

Delivery 1 reads and obeys `verify` on both levels but offers no command to set it (delivery 2). `strict` in delivery 1 means: a published checksum is mandatory and a sender change holds. Delivery 2 adds "attestation mandatory" to it.

#### Merge rules for conflict copies

`Load` today appends apps that only the copy has. New, in a function `merge(dst *Config, other Config)` that `Load` calls per copy (`config` imports `verify` for `Merge` and `Stricter`):

| Field | Rule |
|---|---|
| app only in the copy | append (as today) |
| `Channel`, `AppName` | the main file wins; `AppName` is filled from the copy when empty |
| `Digests` | union of keys. Same key, same value: keep. Same key, different values: `verify.Merge` of both, so the entry holds two digests and counts as contested |
| `BundleID` + `TeamID` | treated as one pair. Main file's pair empty: take the copy's. Both set and different: keep the main file's pair. Never mix the bundle ID of one with the team of the other |
| `App.Verify`, `Config.Verify` | `verify.Stricter` of both |

Why the sender pair is not marked "contested" like a digest: a digest entry is written once and never changes, so two values are a real contradiction. The sender is one mutable value and every `Update` rewrites the whole file, so a conflict copy usually just carries the value from before another Mac accepted a change. Keeping either pair is safe, because a package from the other sender then meets a known sender that differs, which asks (standard) or holds (strict). It can never install silently.

`write` is unchanged (`Digests` is a map; `encoding/json` writes its keys sorted).

The catalog export stays as it is: `store.Catalog` builds `catalog.Entry` from repo, channel and app name only, so digests and senders never leave in an exported catalog.

### `internal/state`

```go
type Installed struct {
	// … unchanged fields …
	// Customs is the report of the check this copy passed; nil for copies
	// installed before the check existed.
	Customs *verify.Report `json:"customs,omitempty"`
}

type State struct {
	Apps map[string]Installed `json:"apps"`
	// Held maps a repository to the report of a package that was not installed
	// (verdict held or ask). It is replaced by the next attempt and removed by
	// a successful install.
	Held map[string]verify.Report `json:"held,omitempty"`
}

func (s *State) Hold(repo string, r verify.Report)
func (s *State) HeldFor(repo string) (verify.Report, bool)
func (s *State) Release(repo string)
```

`Delete` also releases. The reports are stored without `Text` and `Summary` (clear them before saving; `Localize` fills them on the way out).

### `internal/store`

New file `customs.go`; changes in `install.go`, `list.go`, `apps.go`.

```go
// Status gets one field:
	// Customs is the customs note to show: the report of a held package of the
	// newest version, else the report of the installed copy, else nil.
	Customs *verify.Report `json:"customs"`

// Install is unchanged for callers.
func (s *Store) Install(ctx context.Context, repo string, progress Progress) (Status, error)

// AcceptSender installs the package whose sender change is waiting for repo
// (state.Held with verdict ask). The acceptance covers exactly the digest and
// the sender in that report: if the download or the app inside differs, the
// result is a new report and nothing is installed.
func (s *Store) AcceptSender(ctx context.Context, repo string, progress Progress) (Status, error)

// install is what both call, after normalising the repo and taking s.mu.
func (s *Store) install(ctx context.Context, repo string, progress Progress, accepted *verify.Report) (Status, error)
```

Errors: when a package is held or needs an answer, `Install` and `AcceptSender` return the full `Status` (with `Customs` set) **and** an error that wraps `verify.ErrHeld` or `verify.ErrAsk`:

```
Feldspur 1.4.0 was not installed: held at customs: <text of the first failing check>
Feldspur 1.4.0 was not installed: the sender changed and needs your confirmation: was …, now …
```

`AcceptSender` with nothing waiting returns `fmt.Errorf("%s has no sender change waiting for confirmation", repo)`.

Changed and new private functions:

```go
// download also returns the SHA-256 of what it wrote.
func (s *Store) download(ctx context.Context, app config.App, asset forge.Asset, progress Progress) (file, digest string, remove func(), err error)
//   h := sha256.New(); s.Forge.Download(ctx, app.Repo, asset, io.MultiWriter(f, h), cb); verify.FormatDigest(h.Sum(nil))

// checkDownload runs the checks that need only the file's digest. Delivery 2
// appends the attestation check here.
func (s *Store) checkDownload(ctx context.Context, app config.App, rel forge.Release, asset forge.Asset, digest string) []verify.Check

// checksumCheck finds, loads and reads the release's checksum file.
func (s *Store) checksumCheck(ctx context.Context, app config.App, rel forge.Release, asset forge.Asset, digest string) verify.Check

// knownSender is the sender to compare with: the synced list's, else that of
// the copy installed on this computer (by Lieferling or found by app name),
// else the zero value.
func (s *Store) knownSender(ctx context.Context, app config.App, st state.State) verify.Sender

// hold writes a report with verdict held or ask to state.json and, when the
// only digest failure is digest.synced "mismatch", adds this computer's digest
// to the synced entry so the other computers see the contradiction.
func (s *Store) hold(app config.App, rep verify.Report) error

// customs picks the report for Status (see Status.Customs).
func customs(a config.App, st state.State, out Status) *verify.Report

// record gets the report and the sender to write.
func (s *Store) record(app config.App, rel forge.Release, asset forge.Asset, res install.Result, rep verify.Report, sender verify.Sender) (state.State, error)
```

`installRelease` in order:

1. `download`. Re-read the app from `config.Load` afterwards (the copy from before the lock can lack digests another Mac wrote meanwhile) and resolve the policy.
2. Policy `Off`: `Installer.Install(…, nil)`, report `{Verdict: Exempt}`, `record` (writes neither digest nor sender). Done.
3. `rep.Checks = checkDownload(…)`. If `accepted != nil && accepted.Digest != digest`, drop `accepted`.
4. If any check failed: `Decide`, `hold`, return `ErrHeld`. The archive is never opened.
5. `Installer.Install` with an inspect closure that
   - builds `now := verify.Sender{u.Identity.BundleID, u.Identity.TeamID}` and `known := s.knownSender(…)`,
   - appends `verify.SenderCheck(known, now, accepted != nil && accepted.After != nil && accepted.After.Equal(now))`,
   - sets `Before`/`After` when they differ, calls `Decide`,
   - returns `verify.ErrAsk` or `verify.ErrHeld` for those verdicts, else nil.
6. If the error is `ErrAsk` or `ErrHeld`: `hold`, build the status, return it with the wrapped error. Any other error passes through as today, and nothing is written.
7. `record`: `state.Installed.Customs = &rep`, `st.Release(repo)`; in `config.Update`: `AppName` as today, `BundleID`/`TeamID` from `now` when its bundle ID is not empty, `Digests[verify.Key(version, asset.Name)] = digest`, then `pruneDigests`.

`checksumCheck`: `verify.ChecksumFiles` over the release's asset names. None: skip `none`. For each candidate in order: skip it when `Size > verify.MaxChecksumFile`; download into a writer that fails after `MaxChecksumFile` bytes; on error remember `unreadable` and go on; `ParseChecksums(data, asset.Name, verify.IsBare(name, asset.Name))`; when found or ambiguous, return `ChecksumCheck` for that file. If no candidate named the asset: skip `unlisted` (or `unreadable` when none could be loaded), with the first candidate as `Source`.

`pruneDigests(digests map[string]string, keep int)` in `customs.go`: group keys by `verify.SplitKey` version, order versions with `pick.Newer`, keep the entries of the newest `keepVersions = 5` versions. The entry just written always stays.

`customs` (for `status`, called once `Latest` is known, and also on the early error returns with what is known):

1. `held, ok := st.HeldFor(repo)` and `held.Version == out.Latest`: that report. A held report for an older version than the newest is ignored (the next `record` or `hold` removes it).
2. Else the installed copy's `Customs`, when present. If `verify.Seen(a.Digests[verify.Key(in.Version, in.Asset)])` holds more than one digest: append `{digest.synced, fail, "contested"}` and set the verdict to `Disputed`. The app stays installed and usable; the note says what happened.
3. Else nil.

`status` calls `verify.Localize(rep, verify.English)` so the CLI can print it as it is.

`Remove` releases the held report along with the installation.

### `service.go` and the binding

```go
// Install installs or updates one app. A package held at customs is not an
// error here: the returned status carries the note in Customs.
func (s *AppService) Install(ctx context.Context, repo string) (store.Status, error)

// AcceptSender installs the package whose changed sender the user just confirmed.
func (s *AppService) AcceptSender(ctx context.Context, repo string) (store.Status, error)
```

Both end in one helper:

```go
func (s *AppService) installed(ctx context.Context, st store.Status, err error) (store.Status, error) {
	held := errors.Is(err, verify.ErrHeld) || errors.Is(err, verify.ErrAsk)
	if err != nil && !held {
		return st, translate(err)
	}
	s.present(&st)
	if held {
		if st.Self && s.notify != nil {
			s.notify("Lieferling-Update angehalten", st.Customs.Summary)
		}
		s.changed(ctx)
		return st, nil
	}
	// … relaunch for Self, else changed: as today …
}
```

- `present(st *store.Status)` sets `Self` and calls `verify.Localize(st.Customs, verify.German)`; `remember` uses it for every entry, next to `translateAll`.
- `notify func(title, body string)` is a new field that `main.go` sets to the existing `updateChecker.notify`. There is no background install today, so this is the whole of "self-update notifies"; the guarantee that a held package is never placed comes from the inspect callback.
- No new event. The list (`apps:changed`, `Apps()`) carries `customs` for every app, which is what keeps a held note on screen after a refresh or a restart.
- `frontend/bindings` is regenerated; `backend.ts` adds `export type {Check, Report} from "../bindings/github.com/pklaschka/lieferling/internal/verify/models";`. `Status.customs` is `Report | null`.

### Frontend

One new feature module, one block in `index.html`, one stylesheet; `row.ts` does not change. Follow `AGENTS.md`: run the `modern-web-guidance` skill before writing the markup and CSS (`details`, `dialog`) and `impeccable` for the look.

**`index.html`**

- Sprite: add `<symbol id="i-dash" viewBox="0 0 16 16"><path d="M4.5 8h7"/></symbol>` for "not checkable".
- In `#row`, after `.problem`:

```html
<details class="customs" data-section="customs" hidden>
  <summary>
    <svg class="icon chevron" aria-hidden="true"><use href="#i-chevron"/></svg>
    <span class="customs-label">Zoll</span>
    <span class="customs-summary"></span>
  </summary>
  <ul class="customs-checks"></ul>
  <p class="customs-actions" hidden>
    <button type="button" class="button" data-customs="review">Absender ansehen …</button>
  </p>
</details>
```

- A template for one check line:

```html
<template id="customs-check">
  <li>
    <svg class="icon" aria-hidden="true"><use/></svg>
    <span class="visually-hidden"></span>
    <span class="customs-text"></span>
  </li>
</template>
```

- A dialog:

```html
<dialog id="confirm-sender" aria-labelledby="sender-title" aria-describedby="sender-note">
  <form method="dialog">
    <h2 id="sender-title"></h2>
    <p id="sender-note">Die neue Version ist anders gekennzeichnet als die bisherige. Das kommt vor, wenn der Entwickler anfängt zu signieren, sein Zertifikat wechselt oder die App umbenennt. Es kommt auch vor, wenn jemand anderes das Paket gebaut hat. Lieferling kann das nicht unterscheiden.</p>
    <dl class="sender-compare">
      <dt>Bisher</dt>
      <dd data-sender="before"><code class="bundle"></code><span class="team"></span></dd>
      <dt>Jetzt</dt>
      <dd data-sender="after"><code class="bundle"></code><span class="team"></span></dd>
    </dl>
    <div class="dialog-actions">
      <button value="cancel" class="button" autofocus>Nicht installieren</button>
      <button value="accept" class="button danger">Annehmen und installieren</button>
    </div>
  </form>
</dialog>
```

Title: `` `${app.name} ${report.version} kommt von einem anderen Absender` ``. `.team` reads `Team-ID ABCDE12345` or `ohne Team-ID`. The safe choice has the focus; Esc is "Nicht installieren". Count the tags afterwards, as ADR 0001 asks.

**`features/install.ts`**

```ts
export interface InstallOptions {
  accept?: boolean; // call AcceptSender instead of Install
  quiet?: boolean;  // part of "Alle aktualisieren": open no dialog
}
export async function install(repo: string, o?: InstallOptions): Promise<Status | undefined>;
// called after every install that returned a status, held or not
export function onInstalled(fn: (app: Status, o: InstallOptions) => void): void;
```

`install` calls `o.accept ? AppService.AcceptSender(repo) : AppService.Install(repo)`, clears `busy`, then runs the listeners. "Alle aktualisieren" passes `{quiet: true}`. The hook keeps the dependency one way (customs knows install, install knows no feature), like the registries in `row.ts`.

**`features/customs.ts`** (new)

```ts
export function setupCustoms(): void;
```

- `rowSection("customs", {setup, fill})`.
- `fill(el, app)`: `el.hidden = !app.customs`. Otherwise set `el.dataset.verdict`, the summary text, rebuild the check list from the template (`use href`: `#i-check` for pass, `#i-alert` for fail, `#i-dash` for skip; the visually hidden prefix "Bestanden: ", "Nicht bestanden: ", "Nicht prüfbar: "; `data-result` on the `li`), show `.customs-actions` only for `ask`. For `held`, `ask` and `disputed` set `el.open = true`; when the verdict leaves that group, close it; otherwise leave `open` alone so a refresh does not fold what the user opened. Only ever `textContent`.
- `setup(el, row)`: a `click` listener on the `summary` that calls `preventDefault()` while the verdict is `held`, `ask` or `disputed` (a held package is always expanded), and the listener of the review button, which looks the app up with `findApp(row.dataset.repo!)` and calls `review`.
- `review(app)`: fill `#confirm-sender` from `app.customs.before` / `.after`, `await ask(dialog)`, and on `"accept"` call `install(app.repo, {accept: true})`.
- `onInstalled((app, o) => { if (app.customs?.verdict === "ask" && !o.quiet) review(app); })`: a single "Aktualisieren" that runs into a sender change asks at once; during "Alle aktualisieren" the question waits in its row.

`main.ts`: `setupCustoms()` after `setupInstall()`.

**`styles/customs.css`** (new, imported in `index.css` right after `parcel.css`)

- `.customs`: `font-size: var(--step--1)`, `color: var(--ink-2)`. The rule `.parcel > [data-section] { grid-column: 1 / -1; }` already places it.
- `summary`: `display: flex`, `align-items: baseline`, `gap: 0.4rem`, `cursor: pointer`, `list-style: none`, `::-webkit-details-marker { display: none; }`; the chevron turns with `details[open]`. Focus as for links: 2px ink outline.
- `.customs-label`: the field-label voice of the route stops (Archivo, 0.6875rem, width 75%, weight 600, capitals, +0.05em), followed by a middle dot from `::after`.
- `.customs-checks`: no list marker, `display: grid`, `gap: 0.25rem`, lines as `grid-template-columns: 1em minmax(0, 1fr)`; digests and bundle IDs can be long, so `overflow-wrap: anywhere`.
- Colour only through state: `[data-verdict="held"], [data-verdict="ask"], [data-verdict="disputed"]` set the summary and the failing lines (`li[data-result="fail"]`) in `--express`, which is "something failed" in `DESIGN.md`. Passing lines stay in ink; no delivered green, which belongs to the tracking strip. For the three stopped verdicts the summary has `cursor: default` and no chevron.
- `.sender-compare`: a `dl` on a two-column grid in a dashed `--cut` frame like `.command-link`; bundle IDs in `--mono`.
- No new tokens.

`DESIGN.md` gets a "Zollvermerk" paragraph under Components in the same PR.

### Command line

- New flag `--accept-sender` (bool) for `install` and `update`.
- `install` in `commands.go`:
  - after any install that returned a report, print it: `Customs: <summary>` and one line per check, prefixed `  ok    `, `  FAIL  ` or `  --    `;
  - on `errors.Is(err, verify.ErrAsk)`: with `--accept-sender` call `s.AcceptSender` at once; otherwise, when stdin is a terminal, ask `Install it anyway? [y/N] ` and call `s.AcceptSender` on `y`/`yes`; otherwise return the error with the hint `run again with --accept-sender to accept the new sender`.
- Under `strict` a sender change is `ErrHeld`, and the flag does nothing.
- Exit code 3 when the command failed only because packages were held or need an answer (`errors.Is(err, verify.ErrHeld) || errors.Is(err, verify.ErrAsk)`); 1 and 2 as today. `updateApps` wraps `verify.ErrHeld` in its error when every failure was a hold.
- `list`: `(held at customs)` after the newest version when `Customs.Verdict` is `held` or `ask`.
- Testability without changing `run`'s signature: package variables `var stdin io.Reader = os.Stdin` and `var isTerminal = func() bool { … }` (the check `install.DefaultOptions` already makes).
- `help` and the package comment get the flag and one paragraph on the check.

### README

One paragraph under "How it picks what to install" in delivery 1: what is compared, that a held package is not installed, the `--accept-sender` flag and exit code 3. The full chapter is delivery 3.

## Tests (`go test ./internal/... ./cmd/...`)

| Package | Test | What it pins |
|---|---|---|
| `verify` | `TestParseChecksums` | table: GNU text and binary lines, BSD lines, bare digest only with `bare`, `./dir/` prefixes, CRLF, comments, SHA-512 lines skipped, upper-case hex, asset missing, same asset twice with different digests |
| | `TestChecksumFiles` | order and case: per-file before `SHA256SUMS` before `*checksums.txt`; other files' `.sha256` not picked |
| | `TestForgeDigest`, `TestSyncedDigest`, `TestSenderCheck` | every row of the rules above, including `sha512:` as `algorithm` and a two-digest value as `contested` |
| | `TestDecide` | table over policy and check combinations; strict without a published checksum; sender change is `ask` vs. `held` |
| | `TestResolve`, `TestStricter` | app wins; global `off` is standard; unknown is strict |
| | `TestKey`, `TestMerge` | tag with a slash; union is sorted and idempotent |
| | `TestWording` | every (ID, result, code) that the constructors can produce has a German and an English sentence, no placeholder is left after `Localize`, and no sentence of the delivery 1 checks contains "Apple", "notar" or "Gatekeeper" |
| `forge` | `TestAssetDigest` | GitHub JSON with `"digest":"sha256:…"` and with `null`; a Gitea release leaves it empty |
| `install` | `TestParseTeamID` | real `codesign -dv` output for a Developer ID app, an ad-hoc app (`TeamIdentifier=not set`), an unsigned app (error text), a malformed team |
| `config` | `TestMergeDigests` | union; a contradiction becomes a two-digest value; survives `Update` and the copies are removed |
| | `TestMergeSender`, `TestMergePolicy` | pair taken when empty, main pair kept on contradiction, never mixed; stricter policy wins |
| `state` | `TestHeld` | `Hold`, `HeldFor`, `Release`, round trip through `Save`/`Load`, old `state.json` without the fields |
| `store` | `TestInstallRecordsDigestAndSender` | after an install: digest under `version/asset`, bundle ID and team in `config.json`, report in `state.json`, `Status.Customs.Verdict == cleared` |
| | `TestForgeDigestMismatchHolds` | wrong `digest` in the release: `ErrHeld`, the installer was never called, nothing in `config.json`, the temp file is gone, the list shows the held report |
| | `TestChecksumFile` | `SHA256SUMS` matching, mismatching, not naming the asset, larger than 1 MiB |
| | `TestSecondMacSeesAnotherPackage` | two stores sharing `ConfigPath`: the second downloads other bytes for the same version: held there, the entry now holds two digests, the first store's list shows `disputed` |
| | `TestSenderChangeAsks` | installed with sender A, next version carries B: `ErrAsk`, the fake installer placed nothing, `AcceptSender` installs and writes B; `AcceptSender` when the download changed in between does not install |
| | `TestSenderChangeHoldsUnderStrict`, `TestPolicyOff`, `TestStrictNeedsChecksum` | the policy branches |
| | `TestSenderFromInstalledCopy` | no bundle ID in the list, but a copy installed: its identity is the reference |
| | `TestPruneDigests` | six versions in, the five newest stay |
| `cmd/lieferling` | `TestInstallAsksOnTerminal`, `TestAcceptSenderFlag`, `TestHeldExitsWithThree` | prompt with `y` and with `n`, no prompt without a terminal, the flag, the printed report, the exit code mapping (factor the mapping into `exitCode(err) int`) |

Test fixtures in `store_test.go`: `fakeInstaller` gets `identity install.Identity` (what the next package contains), `installedIdentity map[string]install.Identity` (by path) and `placed int`; its `Install` calls `inspect` first and returns its error without recording anything. `newTestStore`'s server serves `downloads[name]` when set, else the "contents of …" default, so tests can add a `SHA256SUMS` asset and swap an asset's bytes.

Not testable on Linux and checked by hand on a Mac before merging: `identify` against a Developer ID app, an ad-hoc signed app and an unsigned app; that a held package leaves the running app running; the dialog and the note in light and dark, at 520px width, with the keyboard only and with VoiceOver. Plus the commands from ADR 0001's verification plan: `GOOS=darwin GOARCH=arm64 go build ./cmd/lieferling`, `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go vet -tags purego .`, regenerated bindings, `npm run build`.

## Order of work (about four days)

1. `internal/verify` with its tests; `forge.Asset.Digest`; `store.download` returns the digest. (Day 1)
2. `config` fields and merge rules; `state` fields. (Day 1 to 2)
3. `install`: `Inspect`, `Unpacked`, `Identity`, `identify`, `parseTeamID`. (Day 2)
4. `store`: `customs.go`, `installRelease`, `record`, `hold`, `AcceptSender`, `Status.Customs`, tests. (Day 2 to 3)
5. `service.go`: `installed`, `present`, `AcceptSender`, `notify`; bindings. (Day 3)
6. Frontend: `customs.ts`, `install.ts` hook, markup, `customs.css`, `DESIGN.md`. (Day 3 to 4)
7. Command line: flag, prompt, report, exit code; README paragraph; the hand checks on a Mac. (Day 4)

If time runs short, cut in this order: the `disputed` note on the first Mac (the second Mac still holds), then the `(held at customs)` marker in `lieferling list`. Do not cut the `unchecked` verdict or the wording test.

One PR, `feat: check checksum and sender before installing (#30)`, commits along the steps above. It publishes a minor release.

## Deferred, with the seam each one uses

**Delivery 2 (attestation, policy commands, `lieferling verify`)**

- A check `attestation` appended in `store.checkDownload`, which already has repo, release, asset and digest. sigstore-go and the forge call for the attestation bundle arrive with it; delivery 1 adds no dependency.
- `verify.Decide` gets the strict rule "attestation must pass" and a `policy` line `attestation_required`.
- `lieferling policy …` writes `Config.Verify` and `App.Verify`, which delivery 1 already reads.
- `lieferling verify <repo>` is `install` with an inspect that finishes the report and then returns a sentinel error, so nothing is placed; it prints the report as `install` does and uses exit code 3.
- Possibly an explicit way to release a package held for a digest contradiction (see "Open points").

**Delivery 3 (signature, notarization, quarantine, UI, README)**

- `install.Identity` grows (`Signed`, `Notarized`, signing authority); `identify` fills it; checks `signature` and `notarization` are appended in the inspect closure. These are the only lines allowed to mention Apple.
- `place` gets a parameter for keeping the quarantine flag (ADR 0001), fed from the report.
- The note already renders any check list; delivery 3 is polish, the policy controls in the app and, if wanted, the fourth stop.

## Answers to the issue's open questions (recommendations)

1. **Where to set the global policy.** Not in a settings window. Delivery 2: `lieferling policy [standard|strict]` and `lieferling policy <repo> standard|strict|off|default`, plus the documented `verify` keys in `config.json`. Delivery 3: a submenu "Zollkontrolle" with two radio items (Standard, Streng) in the app menu, next to "Kommandozeilen-Tool installieren …", and the per-app exception as an entry in the row menu. A native menu holds a two-value setting without a new surface.
2. **Default.** `standard`. Most apps on the list are unsigned, Forgejo and Gitea list no digests, and few releases carry attestations yet; `strict` as default would hold nearly everything on day one and teach the owner to switch the check off. `standard` still stops every contradiction it can see.
3. **Fourth stop "Geprüft".** Not in deliveries 1 and 2; the note is enough. The strip is fixed at three stops of 5.25rem, and placeholders, the narrow layout, the progress legs and the reduced-motion rule all count on three. Decide in delivery 3, when signature lines exist and the full note is designed; a customs stop between "Geladen" and "Installiert" fits the metaphor, and the place to weigh it is with the whole UI in view.
4. **Digest contradiction between two Macs.** Block only the second, tell both. The second Mac holds the package and writes its digest next to the first one's in the synced entry. From then on the version is contested: no Mac installs it, and the Mac that already has it shows the note expanded with verdict `disputed`. Lieferling does not remove or disable the installed copy; it cannot know which of the two packages is the right one.

## Consequences

- From this release on an install can end without installing and without a technical error. The window shows why in the row; the command line exits with 3.
- The synced list grows by a sender and up to five versions of digests per app. An exported catalog carries none of it.
- **Older Lieferling versions drop the new fields.** `config.read` decodes into the struct and `write` encodes it back, so a Mac still on an older version removes `digests`, `bundleId`, `teamId` and `verify` whenever it changes the list. The check then degrades to "first delivery" and "nothing to compare with" (or to the installed copy's identity) and never to a false hold, but the cross-Mac comparison only works once every Mac is updated. The release notes should say so. All config fields of the three deliveries are defined now, so delivery 1 installs do not strip what deliveries 2 and 3 write.
- The first install per app is trust on first use: the first sender seen becomes the known one. The `sender` line says so.
- A checksum file from the same release proves an intact download, not an honest publisher; whoever can replace the asset can replace the file. The line says exactly that. Provenance is delivery 2.
- Accepting a sender change downloads the package a second time.
- `--accept-sender` accepts whichever new sender the package has and prints it. That is the price of a non-interactive flag; under `strict` it has no effect.
- `Installer` gains a parameter and a method; a future Windows or Linux installer must provide `Identity` (the zero value is allowed and reads as "not comparable").
- German sentences for the checks live in `internal/verify`, the one place where the core holds UI wording.
- `identify` adds one or two `codesign` calls per install; on a large Electron bundle that is a second or two.

## Open points

- **Releasing a held package on purpose.** A developer who re-uploads an asset under the same tag after one Mac installed it produces a real `digest.synced` contradiction. Delivery 1 offers no override: the ways out are a new release, the per-app exception `"verify": "off"`, or deleting the entry from `config.json`. Recommendation: keep it that way in delivery 1 and decide with `lieferling verify` in delivery 2 whether a deliberate `--accept-digest sha256:…` is wanted.
- **`unchecked` as its own collapsed line** ("Durchgelassen, es gab nichts zu vergleichen") instead of the issue's single "Geprüft und freigegeben". Recommended; the owner may prefer one line.
- **Blanket `--accept-sender`** versus a value (`--accept-sender=<bundle id>`). The blanket flag is simpler for scripts; the value is stricter.
- **Exit code 3** for held packages, shared later by `lieferling verify`.
