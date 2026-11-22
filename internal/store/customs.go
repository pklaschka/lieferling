package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pklaschka/lieferling/internal/attest"
	"github.com/pklaschka/lieferling/internal/config"
	"github.com/pklaschka/lieferling/internal/forge"
	"github.com/pklaschka/lieferling/internal/pick"
	"github.com/pklaschka/lieferling/internal/state"
	"github.com/pklaschka/lieferling/internal/verify"
)

// keepVersions is how many versions of digests the synced list keeps per app.
const keepVersions = 5

// checkDownload runs the checks that need only the file's digest and adds
// them to rep: the three digest checks, then, when none of them failed, the
// build attestation and the workflow pin. acceptDigest is a digest the user
// accepted against the synced list.
func (s *Store) checkDownload(ctx context.Context, app config.App, rel forge.Release, asset forge.Asset, rep *verify.Report, acceptDigest string) {
	forgeName := "GitHub"
	if r, err := forge.ParseRepo(app.Repo); err == nil {
		forgeName = r.Forge()
	}
	seen := app.Digests[verify.Key(pick.Version(rel.TagName), asset.Name)]
	synced := verify.SyncedDigest(seen, rep.Digest)
	if synced.Result == verify.Fail && acceptDigest != "" && strings.EqualFold(acceptDigest, rep.Digest) {
		synced = verify.SyncedAccepted(seen, rep.Digest)
	}
	rep.Checks = append(rep.Checks,
		verify.ForgeDigest(forgeName, asset.Digest, rep.Digest),
		s.checksumCheck(ctx, app, rel, asset, rep.Digest),
		synced,
	)
	if rep.Failed() {
		return // a damaged or swapped file needs no attestation to be held
	}
	c, origin := s.attestationCheck(ctx, app, forgeName, rep.Digest)
	rep.Checks = append(rep.Checks, c)
	rep.Origin = origin
	rep.Checks = verify.RequireWorkflow(rep.Checks, app.Workflow)
}

// attestationCheck asks the forge for build attestations of the file and
// verifies them.
func (s *Store) attestationCheck(ctx context.Context, app config.App, forgeName, digest string) (verify.Check, *verify.Origin) {
	r, err := forge.ParseRepo(app.Repo)
	if err != nil || r.Host != "" {
		return verify.NotAttested(forgeName, "unsupported", "", ""), nil
	}
	bundles, err := s.Forge.Attestations(ctx, app.Repo, digest)
	switch {
	case errors.Is(err, forge.ErrNoAttestations):
		return verify.NotAttested(forgeName, "unsupported", "", ""), nil
	case err != nil:
		return verify.NotAttested(forgeName, "unreadable", "", err.Error()), nil
	case len(bundles) == 0:
		return verify.NotAttested(forgeName, "none", "", ""), nil
	case s.Attest == nil:
		return verify.NotAttested(forgeName, "trust", "", "no verifier"), nil
	}
	o, err := s.Attest.Verify(ctx, bundles, attest.Expect{Digest: digest, Repo: r.Path(), Workflow: app.Workflow})
	var m *attest.Mismatch
	switch {
	case err == nil:
		return verify.Attested(forgeName, o, app.Workflow != ""), &o
	case errors.Is(err, attest.ErrTrust):
		return verify.NotAttested(forgeName, "trust", "", err.Error()), nil
	case errors.As(err, &m) && errors.Is(m, attest.ErrWorkflow):
		return verify.NotAttested(forgeName, "workflow", m.Want, m.Got), nil
	case errors.As(err, &m):
		return verify.NotAttested(forgeName, "repository", m.Want, m.Got), nil
	}
	return verify.NotAttested(forgeName, "invalid", "", err.Error()), nil
}

// syncedAccepted reports whether the user accepted this package against the
// synced list.
func syncedAccepted(rep verify.Report) bool {
	for _, c := range rep.Checks {
		if c.ID == verify.CheckSynced && c.Code == "accepted" {
			return true
		}
	}
	return false
}

// checksumCheck finds, loads and reads the release's checksum file.
func (s *Store) checksumCheck(ctx context.Context, app config.App, rel forge.Release, asset forge.Asset, digest string) verify.Check {
	names := make([]string, len(rel.Assets))
	byName := map[string]forge.Asset{}
	for i, a := range rel.Assets {
		names[i] = a.Name
		byName[a.Name] = a
	}
	files := verify.ChecksumFiles(names, asset.Name)
	if len(files) == 0 {
		return verify.NoChecksums("none", "")
	}
	loaded := false
	for _, name := range files {
		f := byName[name]
		if f.Size > verify.MaxChecksumFile {
			continue
		}
		var buf bytes.Buffer
		if err := s.Forge.Download(ctx, app.Repo, f, &limitWriter{w: &buf, left: verify.MaxChecksumFile}, nil); err != nil {
			continue
		}
		loaded = true
		sum, found, err := verify.ParseChecksums(buf.Bytes(), asset.Name, verify.IsBare(name, asset.Name))
		if found || err != nil {
			return verify.ChecksumCheck(name, sum, found, err, digest)
		}
	}
	if !loaded {
		return verify.NoChecksums("unreadable", files[0])
	}
	return verify.ChecksumCheck(files[0], "", false, nil, digest)
}

var errTooLarge = errors.New("the checksum file is too large")

// limitWriter fails once more than left bytes are written.
type limitWriter struct {
	w    *bytes.Buffer
	left int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, errTooLarge
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// knownSender is the sender to compare with: the synced list's, else that of
// the copy installed on this computer (by Lieferling or found by app name),
// else the zero value.
func (s *Store) knownSender(ctx context.Context, app config.App, st state.State) verify.Sender {
	if app.BundleID != "" || app.TeamID != "" {
		return verify.Sender{BundleID: app.BundleID, TeamID: app.TeamID}
	}
	path := ""
	if in, ok := st.Get(app.Repo); ok {
		path = in.Path
	} else if _, p := s.Installer.InstalledVersion(app.AppName); p != "" {
		path = p
	}
	if path == "" {
		return verify.Sender{}
	}
	id := s.Installer.Identity(ctx, path)
	return verify.Sender{BundleID: id.BundleID, TeamID: id.TeamID}
}

// hold writes a report with verdict held or ask to state.json. When the only
// digest failure is a mismatch with the synced list, it adds this computer's
// digest to the synced entry, so the other computers see the contradiction.
func (s *Store) hold(app config.App, rep verify.Report) error {
	st, err := state.Load(s.StatePath)
	if err != nil {
		return err
	}
	st.Hold(app.Repo, rep)
	if err := state.Save(s.StatePath, st); err != nil {
		return err
	}
	if !onlySyncedMismatch(rep) {
		return nil
	}
	return config.Update(s.ConfigPath, func(c *config.Config) error {
		a := c.Find(app.Repo)
		if a == nil {
			return nil
		}
		if a.Digests == nil {
			a.Digests = map[string]string{}
		}
		k := verify.Key(rep.Version, rep.Asset)
		a.Digests[k] = verify.Merge(a.Digests[k], rep.Digest)
		return nil
	})
}

func onlySyncedMismatch(rep verify.Report) bool {
	synced := false
	for _, c := range rep.Checks {
		if c.Result != verify.Fail {
			continue
		}
		if c.ID != verify.CheckSynced || c.Code != "mismatch" {
			return false
		}
		synced = true
	}
	return synced
}

// customs picks the report for Status: the report of a held package of the
// newest version, else the report of the installed copy (marked disputed when
// the computers now disagree about its package), else nil.
func customs(a config.App, st state.State, out Status) *verify.Report {
	if held, ok := st.HeldFor(a.Repo); ok && out.Latest != "" && held.Version == out.Latest {
		verify.Localize(&held, verify.English)
		return &held
	}
	in, ok := st.Get(a.Repo)
	if !ok || in.Customs == nil {
		return nil
	}
	rep := *in.Customs
	rep.Checks = append([]verify.Check(nil), rep.Checks...)
	if seen := verify.Seen(a.Digests[verify.Key(in.Version, in.Asset)]); len(seen) > 1 {
		rep.Checks = append(rep.Checks, verify.SyncedDigest(a.Digests[verify.Key(in.Version, in.Asset)], rep.Digest))
		rep.Verdict = verify.Disputed
	}
	verify.Localize(&rep, verify.English)
	return &rep
}

// pruneDigests keeps the entries of the newest keep versions; the entry
// under current always stays.
func pruneDigests(digests map[string]string, keep int, current string) {
	var versions []string
	seen := map[string]bool{}
	for k := range digests {
		v, _ := verify.SplitKey(k)
		if !seen[v] {
			seen[v] = true
			versions = append(versions, v)
		}
	}
	if len(versions) <= keep {
		return
	}
	sort.Slice(versions, func(i, j int) bool { return pick.Newer(versions[i], versions[j]) })
	drop := map[string]bool{}
	for _, v := range versions[keep:] {
		drop[v] = true
	}
	for k := range digests {
		if v, _ := verify.SplitKey(k); drop[v] && k != current {
			delete(digests, k)
		}
	}
}

// heldError explains why a package was not installed.
func heldError(name string, rep verify.Report) error {
	if rep.Verdict == verify.Ask {
		c := rep.FirstFailure()
		detail := ""
		if c != nil {
			detail = ": was " + c.Want + ", now " + c.Got
		}
		return fmt.Errorf("%s %s was not installed: %w%s", name, rep.Version, verify.ErrAsk, detail)
	}
	r := rep
	r.Checks = append([]verify.Check(nil), rep.Checks...)
	verify.Localize(&r, verify.English)
	detail := ""
	if c := r.FirstFailure(); c != nil {
		detail = ": " + c.Text
	}
	return fmt.Errorf("%s %s was not installed: %w%s", name, rep.Version, verify.ErrHeld, detail)
}
