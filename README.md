<p align="center">
  <img src="docs/icon.png" width="128" height="128" alt="">
</p>

<h1 align="center">Lieferling</h1>

<p align="center">
  Installs and updates your own desktop apps from GitHub, Forgejo and Gitea releases.<br>
  A small app store for the (often private, often unsigned) Electron apps you build yourself.
</p>

<p align="center">
  <a href="https://github.com/pklaschka/lieferling/releases/latest"><b>Download for macOS</b></a>
  &nbsp;·&nbsp;
  <a href="#command-line">Command line</a>
  &nbsp;·&nbsp;
  <a href="#links-for-a-readme">Links for a README</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/window-dark.png">
  <img src="docs/window-light.png" alt="The Lieferling window: four apps, one with an update waiting (red), one installed and current (green), one downloading, one whose release cannot be reached.">
</picture>

Add a repository once, then install, update and remove its app with one click or one command. Every app on the list reads like a parcel label: the installed version, the newest one, and a tracking strip from *Erschienen* to *Geladen* to *Installiert*.

- **App with menu bar icon:** one click installs or updates. Lieferling checks for updates in the background every 6 hours and notifies you when there are new ones.
- **Command line:** `lieferling` does the same from the terminal.
- **Synced across your Macs:** the list lives in iCloud Drive (`Lieferling/config.json`), so every Mac on the same iCloud account sees it. What is installed stays per Mac.
- **GitHub, Forgejo and Gitea:** `owner/name` is a GitHub repository; `codeberg.org/owner/name` (or any Forgejo or Gitea server, also with a port) works the same way.
- **Keeps itself current:** put `pklaschka/lieferling` on its own list.

macOS only for now. Windows and Linux are prepared (see `internal/install`) but not built.

## Formerly Elappma

Lieferling was called Elappma until v0.2.1. When it first starts, Lieferling copies the list from iCloud Drive (`Elappma/config.json` → `Lieferling/config.json`) and the local state, and moves its own entry to `pklaschka/lieferling`. An `Elappma.app` that updated itself to Lieferling gets quit and moved to the Bin. The Keychain item "Elappma GitHub", the `ELAPPMA_*` variables and `elappma://` links keep working. The command line is now `lieferling`.

## Install

Download the DMG from the [releases](https://github.com/pklaschka/lieferling/releases) and drag Lieferling into Applications. The app is not signed by Apple, so clear the quarantine flag once:

```sh
xattr -cr /Applications/Lieferling.app
```

Add `pklaschka/lieferling` to its own list and Lieferling keeps itself up to date: an update replaces the app while it runs and then restarts it. Any other app that was open during its update is opened again afterwards.

## Links for a README

A link can put an app on the list and install it. GitHub only links to `https`, so a README links to the landing page, which hands the request to Lieferling:

```markdown
[In Lieferling installieren](https://pklaschka.github.io/lieferling/?repo=owner/name&install=1)
```

| Parameter | |
|---|---|
| `repo` | `owner/name` (GitHub), `host/owner/name` (Forgejo, Gitea) or the repository's URL |
| `install=1` | install right after adding |
| `prerelease=1` | follow release candidates |

Lieferling always asks before adding or installing:

<p align="center"><img src="docs/link-dialog.png" width="560" alt="Lieferling asking whether to add pklaschka/meerkats-cms, with options to install right away and to follow pre-releases."></p>

"Link für die README kopieren" in an app's menu copies such a link for that app. Under the hood the page opens `lieferling://add?repo=owner/name&install=1`, which works anywhere else links to custom schemes are allowed.

The landing page lives in `site/` and is published by the Pages workflow. GitHub Pages has to be switched on once: Settings → Pages → Source: GitHub Actions.

## Catalogs

A catalog is a list of repositories in a file: to set up a new Mac outside your iCloud account, to hand your apps to someone else, or to keep a team's apps in a repository. In the app, "Katalog" in the title bar (or the Ablage menu) exports the list and imports a catalog from a file or an `https` address. Importing shows what is in the catalog first; it adds only the apps that are not on the list yet, leaves the others as they are, and installs nothing.

<p align="center"><img src="docs/catalog-import.png" width="560" alt="Lieferling asking whether to take three apps from the catalog “Team Werkstatt”, with one already on the list and one unreadable entry."></p>

What export writes:

```json
{
  "lieferling": "catalog",
  "version": 1,
  "name": "Team Werkstatt",
  "apps": [
    { "repo": "pklaschka/feldspur", "channel": "stable", "appName": "Feldspur" }
  ]
}
```

`name` and `appName` are optional. Import also takes a bare JSON array of repositories, Lieferling's own `config.json`, or plain text with one repository per line (`#` starts a comment, a channel may follow the repository):

```text
# Team Werkstatt
pklaschka/feldspur
https://github.com/pklaschka/meerkats-cms  prerelease
```

A link to a file's page on GitHub (`github.com/owner/name/blob/...`) is read through its raw link. Catalogs are limited to 1 MB and only loaded over `https`.

## Private repositories

Public repositories work without signing in. For private ones Lieferling needs a GitHub token with read access to the repository's contents. It looks, in this order, at:

1. `GH_TOKEN` or `GITHUB_TOKEN`,
2. the GitHub CLI's login (`gh auth login`),
3. the Keychain item "Lieferling GitHub":

   ```sh
   security add-generic-password -s "Lieferling GitHub" -a github -w
   ```

On a Forgejo or Gitea server (Codeberg too) Lieferling needs a token with read access to repositories, from:

1. the variable `LIEFERLING_TOKEN_<HOST>`, the host in capitals with every other character as `_` (`LIEFERLING_TOKEN_CODEBERG_ORG`, `LIEFERLING_TOKEN_GIT_EXAMPLE_COM_3000`),
2. the Keychain item "Lieferling *host*":

   ```sh
   security add-generic-password -s "Lieferling codeberg.org" -a token -w
   ```

A token only ever goes to its own server. Tokens are never synced; set them up on each Mac.

Build attestations of a private repository need a token that may read them: a classic token with `repo`, or a fine-grained token with the repository permission "Attestations: read". Without it the customs note says the attestation could not be read.

## Command line

```text
lieferling list                         apps on the list, installed and newest version
lieferling add <repo> [--prerelease]    put a repository on the list
lieferling install <repo>               install (adds it to the list if needed)
lieferling update [<repo>...]           update the given apps, or every app with an update
lieferling verify <repo> [--strict]     check the newest package at customs, install nothing
lieferling policy [<repo>] [standard|strict|off|default] [--workflow <file>]
                                        show or set the customs policy
lieferling open <repo>                  start the installed app
lieferling channel <repo> stable|prerelease
lieferling remove <repo> [--uninstall]  take it off the list (and off this computer)
lieferling export [<file>]              write the list as a catalog (to stdout without a file)
lieferling import <file|url|-> [--dry-run]
                                        add the apps of a catalog to the list
lieferling paths                        where the shared and the local files are
```

`<repo>` is `owner/name` for GitHub, `host/owner/name` for a Forgejo or Gitea server (`codeberg.org/owner/name`), or the repository's web, clone or SSH URL. Servers are always reached over `https`.

The app carries the command line. Choose **Lieferling → Kommandozeilen-Tool installieren …** (or the button in the window's footer) and Lieferling links `/usr/local/bin/lieferling` to `Lieferling.app/Contents/Helpers/lieferling`; macOS asks for an administrator password. The command then updates with the app. An older `lieferling` in `/usr/local/bin`, say from a release download, is replaced only when you confirm. The same dialog removes the link again.

Without the app, download `lieferling-cli-<version>-darwin-<arch>.tar.gz` from the releases and put `lieferling` on your `PATH`.

## How it picks what to install

- **Release:** the newest non-draft release; on the `stable` channel release candidates ("pre-releases") are skipped.
- **File:** the release asset that fits this Mac. A `.dmg` beats a `.zip`; a file naming this processor (`arm64`, `x64`, …) beats a `universal` one, which beats one naming no processor. On Apple Silicon an Intel-only build is the last resort (it runs through Rosetta). Files naming another system are never picked, and a bare `.zip` only when it names macOS.
- **Customs:** before anything is replaced, Lieferling checks the package. The SHA-256 of the download is compared with the checksum the forge lists (GitHub does for newer releases), with a checksum file in the same release (`SHA256SUMS`, `checksums.txt`, `<file>.sha256`) and with what your other Macs saw for the same version (kept in the shared list). The app's bundle ID and team ID are compared with the last version. A package that fails is not installed; the row says why, and `lieferling install`/`update` exit with 3. When only the sender changed, the app and the command line ask first (`--accept-sender` answers yes in scripts). The checksums show that the download is intact and unchanged, not who built it. For that, Lieferling also reads the build attestation GitHub keeps for a release file built by GitHub Actions (`actions/attest-build-provenance`) and checks with Sigstore that it was built in the app's own repository. A repository that was renamed or moved signs its attestations under the new name, so its packages are held until you remove the app and add it under that name. `lieferling policy strict` makes a checksum and an attestation mandatory for every app; `lieferling policy <repo> standard` exempts one, `--workflow release.yml` requires one workflow. `lieferling verify <repo>` runs the check without installing and exits with 3 when the package would be held. Lieferling's own releases carry attestations.
- **Install:** like the original script. Quit the running app (it may ask about unsaved work), replace the copy in `/Applications` and clear the quarantine flag. When `/Applications` is not writable, Lieferling asks for an administrator password.

If two Macs change the list at the same time, iCloud keeps both files ("config 2.json"); Lieferling merges them the next time it writes. An app removed on one Mac can come back that way, so remove it again.

## Development

Requires Go 1.26, Node 22 and the Wails CLI (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`).

```sh
wails3 dev                         # the app with hot reload
go test ./internal/... ./cmd/...   # the core
go run ./cmd/lieferling list          # the command line
wails3 task darwin:package         # Lieferling.app in bin/ (on a Mac)
```

`LIEFERLING_CONFIG`, `LIEFERLING_STATE` and `LIEFERLING_APPLICATIONS` move the shared list, the local state and the install folder, which helps when trying things out.

Releases are automatic: every merge into `main` is released. The release workflow picks the version from the commits since the last release (`scripts/next-version.sh`), builds the universal DMG and the command line, and publishes them as a GitHub release.

| In a commit message | Version |
|---|---|
| `feat!:`, `BREAKING CHANGE:` or `[major]` | major (before 1.0.0: minor) |
| `feat:`, `feat(scope):` or `[minor]` | minor |
| anything else | patch |

With squash merges the PR title is the commit message, so put the marker there.

## Layout

| Path | What |
|---|---|
| `internal/forge` | releases and downloads from GitHub, Forgejo and Gitea, token lookup |
| `internal/pick` | channel, version and asset choice |
| `internal/config` | the synced list (iCloud Drive) |
| `internal/state` | what is installed on this computer |
| `internal/install` | putting an app in place (macOS so far) |
| `internal/verify` | the customs check: policy, checks, verdicts and their wording |
| `internal/attest` | build attestations checked with Sigstore (the only user of sigstore-go) |
| `internal/store` | the core both front ends use |
| `internal/catalog` | catalog files: reading, writing, loading from a URL |
| `internal/deeplink` | `lieferling://` links |
| `site/` | the landing page for README links (GitHub Pages) |
| `docs/` | images for this README |
| `build/appicon.icon`, `build/appicon.png` | the app icon (Icon Composer layers and the classic icon) |
| `cmd/lieferling` | the command line |
| `main.go`, `service.go`, `frontend/` | the Wails app |

## License

Copyright (c) 2026 Zuri Nimca Klaschka. Licensed under the [European Union Public Licence v. 1.2](LICENSE) (EUPL-1.2).
