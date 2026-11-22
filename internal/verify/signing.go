package verify

// Signing is what the installer read from the bundle's code signature
// (install.Identity, copied here so verify stays a leaf).
type Signing struct {
	State     string // "", "adhoc", "untrusted", "unknown", "valid", "invalid" (install.Signature)
	Authority string // "Developer ID Application: Jane Doe (ABCDE12345)"
	Problem   string // codesign's or spctl's complaint
	Assessed  bool   // spctl gave a usable answer
	Notarized bool
	Revoked   bool
	Stapled   bool
}

// SigningChecks gives the signature check and, unless the package is held
// by it, the notarization check. These are the only checks whose sentences
// name Apple, and each names the tool that answered.
//
//	signature:    skip "unsigned" | skip "adhoc" | skip "untrusted" | skip "unassessed"
//	              | pass "valid" (Got authority)
//	              | fail "invalid" (Want authority, Got problem) | fail "revoked" (Got authority)
//	notarization: pass "notarized" | pass "stapled" | skip "unnotarized"
//	              | skip "unsigned" | skip "unassessed"
func SigningChecks(s Signing) []Check {
	sig := Check{ID: CheckSignature}
	note := Check{ID: CheckNotarization, Result: Skip}
	switch {
	case s.State == "adhoc":
		sig.Result, sig.Code = Skip, "adhoc"
		note.Code = "unsigned"
	case s.State == "untrusted":
		sig.Result, sig.Code = Skip, "untrusted"
		note.Code = "unsigned"
	case s.State == "unknown":
		// without a signature there is nothing to say about notarization
		return []Check{{ID: CheckSignature, Result: Skip, Code: "unassessed"}}
	case s.State != "valid" && s.State != "invalid":
		sig.Result, sig.Code = Skip, "unsigned"
		note.Code = "unsigned"
	case s.State == "invalid":
		return []Check{{ID: CheckSignature, Result: Fail, Code: "invalid", Want: s.Authority, Got: s.Problem}}
	case s.Revoked:
		return []Check{{ID: CheckSignature, Result: Fail, Code: "revoked", Got: s.Authority}}
	default:
		sig.Result, sig.Code, sig.Got = Pass, "valid", s.Authority
		switch {
		case !s.Assessed:
			note.Code = "unassessed"
		case s.Notarized && s.Stapled:
			note.Result, note.Code = Pass, "stapled"
		case s.Notarized:
			note.Result, note.Code = Pass, "notarized"
		default:
			note.Code = "unnotarized"
		}
	}
	return []Check{sig, note}
}
