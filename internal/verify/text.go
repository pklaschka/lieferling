package verify

import "strings"

// Lang is the language Localize writes in.
type Lang int

const (
	English Lang = iota
	German
)

// The German sentences live here rather than in the app's translate.go on
// purpose: the rule "every line says exactly what was checked, and nothing
// sounds like Apple checked it" is only enforceable if a test sees every
// sentence (see TestWording).

type phrase struct{ en, de string }

func (p phrase) in(lang Lang) string {
	if lang == German {
		return p.de
	}
	return p.en
}

// summaries is the collapsed line per verdict. The window prints "Zoll" in
// front, the command line "Customs: ".
var summaries = map[Verdict]phrase{
	Cleared:   {"checked and cleared", "Geprüft und freigegeben"},
	Unchecked: {"let through, there was nothing to compare with", "Durchgelassen, es gab nichts zu vergleichen"},
	Ask:       {"{version} held: the sender changed", "Angehalten, nichts wurde installiert"},
	Held:      {"{version} held and not installed", "Angehalten, nichts wurde installiert"},
	Disputed:  {"disputed: another Mac saw a different package for this version", "Widerspruch zwischen deinen Macs"},
	Exempt:    {"not checked, switched off for this app", "Nicht geprüft, für diese App abgeschaltet"},
}

type textKey struct {
	id     string
	result Result
	code   string
}

// sentences is one line per check outcome.
var sentences = map[textKey]phrase{
	{CheckForgeDigest, Pass, "match"}: {
		"The SHA-256 of the downloaded file matches the one {source} lists for this file.",
		"Die SHA-256-Prüfsumme der geladenen Datei stimmt mit der überein, die {source} für diese Datei führt.",
	},
	{CheckForgeDigest, Fail, "mismatch"}: {
		"The SHA-256 of the downloaded file differs from the one {source} lists for this file. The download is damaged or was changed on the way.",
		"Die Prüfsumme der geladenen Datei weicht von der ab, die {source} für diese Datei führt. Der Download ist beschädigt oder wurde unterwegs verändert.",
	},
	{CheckForgeDigest, Skip, "none"}: {
		"{source} lists no checksum for this file.",
		"{source} führt für diese Datei keine Prüfsumme.",
	},
	{CheckForgeDigest, Skip, "algorithm"}: {
		"{source} lists a checksum in a format Lieferling does not know.",
		"{source} führt eine Prüfsumme in einem Verfahren, das Lieferling nicht kennt.",
	},
	{CheckChecksums, Pass, "match"}: {
		"The checksum matches the entry in {source} from the same release. This shows the download is intact, not who built it.",
		"Die Prüfsumme stimmt mit dem Eintrag in {source} aus demselben Release überein. Das zeigt, dass der Download unversehrt ist, nicht, wer ihn gebaut hat.",
	},
	{CheckChecksums, Fail, "mismatch"}: {
		"The checksum differs from the entry in {source} from the same release.",
		"Die Prüfsumme weicht vom Eintrag in {source} aus demselben Release ab.",
	},
	{CheckChecksums, Fail, "ambiguous"}: {
		"{source} names this file more than once with different checksums.",
		"{source} nennt diese Datei mehrfach mit verschiedenen Prüfsummen.",
	},
	{CheckChecksums, Skip, "none"}: {
		"The release has no checksum file.",
		"Das Release enthält keine Prüfsummendatei.",
	},
	{CheckChecksums, Skip, "unlisted"}: {
		"{source} does not name this file.",
		"{source} nennt diese Datei nicht.",
	},
	{CheckChecksums, Skip, "unreadable"}: {
		"{source} could not be loaded.",
		"{source} ließ sich nicht laden.",
	},
	{CheckSynced, Pass, "match"}: {
		"The same package your Macs have seen for this version before.",
		"Dasselbe Paket, das deine Macs für diese Version schon gesehen haben.",
	},
	{CheckSynced, Fail, "mismatch"}: {
		"One of your Macs saw a different package for the same version. The release was replaced after it was published.",
		"Für dieselbe Version hat einer deiner Macs ein anderes Paket gesehen. Das Release wurde nachträglich ausgetauscht.",
	},
	{CheckSynced, Fail, "contested"}: {
		"Your Macs saw different packages for this version.",
		"Deine Macs haben für diese Version verschiedene Pakete gesehen.",
	},
	{CheckSynced, Skip, "first"}: {
		"None of your Macs has installed this version yet, there is nothing to compare with.",
		"Diese Version hat noch keiner deiner Macs installiert, es gibt nichts zu vergleichen.",
	},
	{CheckSender, Pass, "same"}: {
		"The same sender as before: {got}.",
		"Derselbe Absender wie bisher: {got}.",
	},
	{CheckSender, Pass, "accepted"}: {
		"The sender changed and you accepted it: was {want}, now {got}.",
		"Der Absender hat sich geändert, und du hast ihn angenommen: bisher {want}, jetzt {got}.",
	},
	{CheckSender, Fail, "changed"}: {
		"The sender changed: was {want}, now {got}.",
		"Der Absender hat sich geändert: bisher {want}, jetzt {got}.",
	},
	{CheckSender, Skip, "first"}: {
		"First delivery of this app. The sender {got} counts as known from now on.",
		"Erste Lieferung dieser App. Der Absender {got} gilt ab jetzt als bekannt.",
	},
	{CheckSender, Skip, "unreadable"}: {
		"The package names no bundle ID, the sender cannot be compared.",
		"Das Paket nennt keine Bundle-ID, der Absender lässt sich nicht vergleichen.",
	},
	{CheckSynced, Pass, "accepted"}: {
		"One of your Macs saw a different package for this version, and you accepted this one.",
		"Einer deiner Macs hat für diese Version ein anderes Paket gesehen, und du hast dieses angenommen.",
	},
	{CheckAttestation, Pass, "verified"}: {
		"GitHub holds a signed build attestation for exactly this file: built by {workflow} from {ref} at commit {commit} in {repo}. This shows where the file was built, not that its code is harmless.",
		"GitHub führt für genau diese Datei einen signierten Herkunftsnachweis: gebaut von {workflow} aus {ref} beim Commit {commit} in {repo}. Das zeigt, wo die Datei gebaut wurde, nicht, dass ihr Code harmlos ist.",
	},
	{CheckAttestation, Pass, "pinned"}: {
		"GitHub holds a signed build attestation for exactly this file: built by {workflow} from {ref} at commit {commit} in {repo}. That is the workflow you require.",
		"GitHub führt für genau diese Datei einen signierten Herkunftsnachweis: gebaut von {workflow} aus {ref} beim Commit {commit} in {repo}. Das ist der Workflow, den du verlangst.",
	},
	{CheckAttestation, Fail, "invalid"}: {
		"GitHub holds a build attestation for this file, but its signature could not be confirmed.",
		"GitHub führt für diese Datei einen Herkunftsnachweis, aber seine Signatur ließ sich nicht bestätigen.",
	},
	{CheckAttestation, Fail, "repository"}: {
		"The build attestation for this file comes from another repository: expected {want}, found {got}.",
		"Der Herkunftsnachweis für diese Datei stammt aus einem anderen Repository: erwartet {want}, gefunden {got}.",
	},
	{CheckAttestation, Fail, "workflow"}: {
		"This file was built by {got}, and you require {want}.",
		"Diese Datei wurde von {got} gebaut, und du verlangst {want}.",
	},
	{CheckAttestation, Skip, "none"}: {
		"{source} holds no build attestation for this file.",
		"{source} führt für diese Datei keinen Herkunftsnachweis.",
	},
	{CheckAttestation, Skip, "unsupported"}: {
		"{source} issues no build attestations, so where this file was built cannot be checked.",
		"{source} stellt keine Herkunftsnachweise aus, wo diese Datei gebaut wurde, lässt sich also nicht prüfen.",
	},
	{CheckAttestation, Skip, "unreadable"}: {
		"{source} did not hand out the build attestations for this file.",
		"{source} hat die Herkunftsnachweise für diese Datei nicht herausgegeben.",
	},
	{CheckAttestation, Skip, "trust"}: {
		"A build attestation exists, but the Sigstore trust root needed to check it could not be loaded.",
		"Es gibt einen Herkunftsnachweis, aber die Sigstore-Vertrauensanker zum Prüfen ließen sich nicht laden.",
	},
	{CheckPolicy, Fail, "attestation_required"}: {
		"The strict policy requires a confirmed build attestation and there is none.",
		"Die strenge Kontrolle verlangt einen bestätigten Herkunftsnachweis, es gibt keinen.",
	},
	{CheckPolicy, Fail, "workflow_required"}: {
		"You require builds from {want} for this app, and no build attestation could be confirmed.",
		"Du verlangst für diese App Builds aus {want}, und es ließ sich kein Herkunftsnachweis bestätigen.",
	},
	{CheckPolicy, Fail, "checksum_required"}: {
		"The strict policy requires a published checksum and the release has none.",
		"Die strenge Kontrolle verlangt eine veröffentlichte Prüfsumme, das Release hat keine.",
	},
}

// Localize fills Summary and every Check.Text in lang. A nil report is left
// alone.
func Localize(r *Report, lang Lang) {
	if r == nil {
		return
	}
	r.Summary = strings.ReplaceAll(summaries[r.Verdict].in(lang), "{version}", r.Version)
	for i := range r.Checks {
		r.Checks[i].Text = sentence(r.Checks[i], r.Origin, lang)
	}
}

func sentence(c Check, o *Origin, lang Lang) string {
	p, ok := sentences[textKey{c.ID, c.Result, c.Code}]
	if !ok {
		return ""
	}
	want, got := c.Want, c.Got
	if c.ID == CheckSender {
		want, got = senderIn(want, lang), senderIn(got, lang)
	}
	var workflow, ref, commit, repo string
	if o != nil {
		workflow, ref, commit, repo = o.Workflow, o.ShortRef(), o.ShortCommit(), o.Repository
		if o.Signer != "" {
			workflow += map[Lang]string{English: " (signed by ", German: " (signiert von "}[lang] + o.Signer + ")"
		}
	}
	return strings.NewReplacer("{source}", c.Source, "{want}", want, "{got}", got,
		"{workflow}", workflow, "{ref}", ref, "{commit}", commit, "{repo}", repo).Replace(p.in(lang))
}

// senderIn rewrites a Sender.String() in lang.
func senderIn(s string, lang Lang) string {
	if lang != German {
		return s
	}
	return strings.NewReplacer(" (team ", " (Team-ID ", " (no team ID)", " (ohne Team-ID)", "no bundle ID", "ohne Bundle-ID").Replace(s)
}
