package install

import (
	"testing"
	"time"
)

func TestParseTeamID(t *testing.T) {
	for name, c := range map[string]struct{ out, want string }{
		"developer id": {`Executable=/Applications/Feldspur.app/Contents/MacOS/Feldspur
Identifier=de.pklaschka.feldspur
Format=app bundle with Mach-O thin (arm64)
CodeDirectory v=20500 size=1234 flags=0x10000(runtime) hashes=28+7 location=embedded
Signature size=9046
Authority=Developer ID Application: Example GmbH (ABCDE12345)
Authority=Developer ID Certification Authority
Authority=Apple Root CA
Timestamp=21. Nov 2026 at 10:00:00
Info.plist entries=30
TeamIdentifier=ABCDE12345
Runtime Version=14.0.0
Sealed Resources version=2 rules=13 files=40
Internal requirements count=1 size=212`, "ABCDE12345"},
		"ad hoc": {`Executable=/tmp/App.app/Contents/MacOS/App
Identifier=com.example.app
Signature=adhoc
TeamIdentifier=not set`, ""},
		"unsigned":  {`/tmp/App.app: code object is not signed at all`, ""},
		"malformed": {`TeamIdentifier=abc"; rm -rf /`, ""},
	} {
		if got := parseTeamID(c.out); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestParseSignature(t *testing.T) {
	for name, c := range map[string]struct {
		out           string
		signed, adhoc bool
		authority     string
	}{
		"developer id":           {"Identifier=de.example\nAuthority=Developer ID Application: Example GmbH (ABCDE12345)\nAuthority=Developer ID Certification Authority\nAuthority=Apple Root CA\nTeamIdentifier=ABCDE12345", true, false, "Developer ID Application: Example GmbH (ABCDE12345)"},
		"ad hoc":                 {"Identifier=com.example.app\nSignature=adhoc\nTeamIdentifier=not set", false, true, ""},
		"linker signed electron": {"Executable=/tmp/E.app/Contents/MacOS/E\nIdentifier=Electron\nCodeDirectory v=20400 size=500 flags=0x20002(adhoc,linker-signed) hashes=12+0 location=embedded\nSignature=adhoc\nInfo.plist=not bound\nTeamIdentifier=not set\nSealed Resources=none", false, true, ""},
		"unsigned":               {"/tmp/App.app: code object is not signed at all", false, false, ""},
		"empty":                  {"", false, false, ""},
	} {
		signed, adhoc, authority := parseSignature(c.out)
		if signed != c.signed || adhoc != c.adhoc || authority != c.authority {
			t.Errorf("%s: %v %v %q", name, signed, adhoc, authority)
		}
	}
}

func TestParseAssessment(t *testing.T) {
	for name, c := range map[string]struct {
		out                       string
		accepted, revoked, usable bool
		source                    string
	}{
		"notarized":           {"/Applications/A.app: accepted\nsource=Notarized Developer ID\norigin=Developer ID Application: Example GmbH (ABCDE12345)", true, false, true, "Notarized Developer ID"},
		"unnotarized":         {"/Applications/A.app: rejected\nsource=Unnotarized Developer ID\norigin=Developer ID Application: Example GmbH (ABCDE12345)", false, false, true, "Unnotarized Developer ID"},
		"no usable signature": {"/tmp/A.app: rejected (the code is valid but does not seem to be an app)\nsource=no usable signature", false, false, true, "no usable signature"},
		"revoked":             {"/tmp/A.app: CSSMERR_TP_CERT_REVOKED", false, true, false, ""},
		"gatekeeper off":      {"/tmp/A.app: accepted\noverride=security disabled", false, false, false, ""},
		"nothing":             {"", false, false, false, ""},
	} {
		accepted, source, revoked, usable := parseAssessment(c.out)
		if accepted != c.accepted || source != c.source || revoked != c.revoked || usable != c.usable {
			t.Errorf("%s: %v %q %v %v", name, accepted, source, revoked, usable)
		}
	}
}

func TestQuarantineFor(t *testing.T) {
	notarized := Identity{Signature: Valid, Notarized: true, Assessed: true}
	for name, c := range map[string]struct {
		id   Identity
		mark bool
		want Quarantine
	}{
		"unsigned":             {Identity{}, true, Cleared},
		"ad hoc":               {Identity{Signature: AdHoc}, true, Cleared},
		"valid, not notarized": {Identity{Signature: Valid, Assessed: true}, true, Cleared},
		"notarized, left":      {notarized, false, Left},
		"notarized, marked":    {notarized, true, Marked},
		"revoked":              {Identity{Signature: Valid, Notarized: true, Revoked: true}, true, Cleared},
		"invalid":              {Identity{Signature: Invalid, Notarized: true}, true, Cleared},
	} {
		if got := QuarantineFor(c.id, c.mark); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestQuarantineValue(t *testing.T) {
	if got := quarantineValue(time.Unix(0x6740a1b2, 0)); got != "0081;6740a1b2;Lieferling;" {
		t.Errorf("value = %q", got)
	}
}
