package install

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Signature is what the code signature of a bundle amounts to.
type Signature string

const (
	Unsigned Signature = ""        // no signature at all
	AdHoc    Signature = "adhoc"   // signed without a certificate: names no one
	Valid    Signature = "valid"   // signed with a certificate, and codesign --verify --deep --strict passes
	Invalid  Signature = "invalid" // signed with a certificate, and verification fails

	// signed with a certificate macOS does not trust (self-signed, say):
	// it names no one, like ad hoc
	Untrusted Signature = "untrusted"
	// codesign gave no answer: it is missing, timed out or the install was
	// cancelled
	Unknown Signature = "unknown"
)

// Identity is what an app bundle says about itself.
type Identity struct {
	BundleID string // CFBundleIdentifier
	TeamID   string // Developer ID team, only when codesign confirms it; else ""

	// The rest is filled by Install only, not by Installer.Identity: it
	// takes spctl, which may ask Apple and must not run on every list.
	Signature Signature
	Authority string // first "Authority=" line: "Developer ID Application: Jane Doe (ABCDE12345)"
	Problem   string // codesign's or spctl's last line when Invalid or Revoked
	Assessed  bool   // spctl gave an answer that can be used
	Notarized bool   // spctl accepts it with source=Notarized Developer ID
	Revoked   bool   // the signing certificate was revoked
	Source    string // spctl's "source=" value
	Stapled   bool   // a notarization ticket is attached to the bundle
}

// Notarized apps are left as they are: Lieferling neither clears nor sets
// their quarantine flag (see Options.MarkNotarized).
func (i Identity) notarizedApp() bool { return i.Signature == Valid && i.Notarized && !i.Revoked }

// Quarantine is what place does with the com.apple.quarantine flag.
type Quarantine string

const (
	Cleared Quarantine = "cleared" // xattr -cr, so macOS opens an app it would refuse
	Left    Quarantine = "left"    // attributes left as they came (a notarized app)
	Marked  Quarantine = "marked"  // flag set, so Gatekeeper checks the app at first launch
)

// QuarantineFor is what place does for an app with this identity: a valid,
// notarized signature is left alone, or marked when mark is set; anything
// else is cleared as before, since macOS would refuse to open it.
func QuarantineFor(i Identity, mark bool) Quarantine {
	switch {
	case !i.notarizedApp():
		return Cleared
	case mark:
		return Marked
	}
	return Left
}

// parseSignature reads codesign -dv --verbose=4: unsigned for "code object
// is not signed at all", ad hoc for "Signature=adhoc"; else signed is true
// when there is an "Authority=" line, whose first value is returned.
func parseSignature(out string) (signed, adhoc bool, authority string) {
	if strings.Contains(out, "not signed at all") {
		return false, false, ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "Signature=adhoc" {
			adhoc = true
		}
		if v, ok := strings.CutPrefix(line, "Authority="); ok && authority == "" {
			authority = strings.TrimSpace(v)
		}
	}
	if adhoc {
		return false, true, ""
	}
	return authority != "", false, authority
}

// parseAssessment reads spctl --assess --type execute -vv: accepted is the
// word after "<path>: ", source the "source=" value, revoked whether the
// certificate was revoked. usable is false when Gatekeeper is switched off
// ("override=") or the output holds neither "accepted" nor "rejected".
func parseAssessment(out string) (accepted bool, source string, revoked, usable bool) {
	revoked = strings.Contains(out, "CERT_REVOKED") || strings.Contains(out, "has been revoked")
	verdict := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "source="); ok {
			source = strings.TrimSpace(v)
		}
		if strings.HasPrefix(line, "override=") {
			return false, source, revoked, false
		}
		if strings.Contains(line, "=") {
			continue // origin=…, source=…: a name may hold the words
		}
		if i := strings.LastIndex(line, ": "); i >= 0 {
			switch w := strings.Fields(line[i+2:]); {
			case len(w) > 0 && w[0] == "accepted":
				accepted, verdict = true, true
			case len(w) > 0 && w[0] == "rejected":
				verdict = true
			}
		}
	}
	return accepted, source, revoked, verdict
}

// verifyFailure sorts a failed codesign --verify: "revoked" when the
// certificate was revoked, "untrusted" when it does not lead to a root macOS
// trusts, else "" (the code or the signature was changed).
func verifyFailure(out string) string {
	switch {
	case strings.Contains(out, "CERT_REVOKED") || strings.Contains(out, "has been revoked"):
		return "revoked"
	case strings.Contains(out, "CSSMERR_TP_NOT_TRUSTED"):
		return "untrusted"
	}
	return ""
}

// quarantineValue is the value of com.apple.quarantine for an app Lieferling
// put in place at t: "0081;<seconds since 1970 in hex>;Lieferling;".
func quarantineValue(t time.Time) string {
	return fmt.Sprintf("0081;%08x;Lieferling;", t.Unix())
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

var teamPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// parseTeamID finds "TeamIdentifier=ABCDE12345" in codesign's output. "not
// set" and anything that is not ten characters of A-Z and 0-9 give "". The
// check also makes the team safe to put into a code requirement.
func parseTeamID(codesignOutput string) string {
	for _, line := range strings.Split(codesignOutput, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok {
			v = strings.TrimSpace(v)
			if teamPattern.MatchString(v) {
				return v
			}
			return ""
		}
	}
	return ""
}
