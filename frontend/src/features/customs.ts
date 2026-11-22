import {type Check, type Report, type Sender, type Status} from "../backend";
import {ask} from "../lib/dialog";
import {$, clone} from "../lib/dom";
import {findApp} from "../state";
import {install, onInstalled} from "./install";
import {rowSection} from "./row";

// The customs note under a parcel ("Zollvermerk"), drawn like the delivery
// note of #29: one printed line »ZOLL«, the verdict and a tag, unfolding to a
// box with one line per check: Prüfsumme, Herkunft, Absender (later
// Apple-Signatur). A held package and a sender change are always open.
//
// The store reports single checks (package verify); this module folds the
// three digest checks into the one »Prüfsumme« line the user reads.

const lineTemplate = $<HTMLTemplateElement>("#customs-line");
const senderDialog = $<HTMLDialogElement>("#confirm-sender");

type Result = "pass" | "fail" | "skip";
interface Line {
  name: string;
  result: Result;
  detail: (string | Node)[]; // text and <code> pieces
}

const icon: Record<Result, string> = {pass: "#i-check", fail: "#i-alert", skip: "#i-more"};
const said: Record<Result, string> = {pass: "stimmt", fail: "weicht ab", skip: "nicht prüfbar"};

function stopped(r: Report | null | undefined) {
  return r?.verdict === "held" || r?.verdict === "ask" || r?.verdict === "disputed";
}

export function setupCustoms() {
  rowSection("customs", {setup, fill});
  onInstalled((app, o) => {
    if (app.customs?.verdict === "ask" && !o.quiet) void review(app);
  });
}

const reports = new WeakMap<HTMLElement, Report>();

function setup(el: HTMLElement, row: HTMLLIElement) {
  const details = el as HTMLDetailsElement;
  // Enter and Space on a summary dispatch click too, so this covers the keyboard
  $("summary", details).addEventListener("click", (e) => {
    if (stopped(reports.get(details))) e.preventDefault();
  });
  $<HTMLButtonElement>('[data-customs="accept"]', details).addEventListener("click", () => {
    const app = findApp(row.dataset.repo!);
    if (app) void review(app);
  });
}

function fill(el: HTMLElement, app: Status) {
  const details = el as HTMLDetailsElement;
  const r = app.customs;
  details.hidden = !r;
  if (!r) {
    reports.delete(details);
    return;
  }
  const was = stopped(reports.get(details));
  reports.set(details, r);
  details.dataset.verdict = r.verdict;
  $(".customs-summary", details).textContent = r.summary;

  const lines = linesOf(r, app);
  $<HTMLUListElement>(".customs-lines", details).replaceChildren(...lines.map(lineElement));
  showTag($(".customs-tag", details), r, lines);

  const foot = $<HTMLElement>(".customs-foot", details);
  const hint = r.verdict === "ask" ? "Nur fortfahren, wenn du weißt, warum sich der Absender geändert hat." :
    r.verdict === "held" ? "Lieferling hat die Datei verworfen." :
    r.verdict === "disputed" ? "Die installierte Version bleibt, bis eine neue erscheint." : "";
  foot.hidden = !hint;
  $(".customs-hint", foot).textContent = hint;
  $<HTMLElement>('[data-customs="accept"]', foot).hidden = r.verdict !== "ask";

  const summary = $<HTMLElement>("summary", details);
  if (stopped(r)) {
    details.open = true;
    summary.setAttribute("aria-disabled", "true");
    summary.tabIndex = -1; // nothing to toggle, so no tab stop
  } else {
    summary.removeAttribute("aria-disabled");
    summary.tabIndex = 0;
    // leaving a stopped verdict folds the note; otherwise a refresh keeps
    // whatever the user opened
    if (was) details.open = false;
  }
}

// The tag after the verdict: how many lines passed, or which one stopped it.
function showTag(tag: HTMLElement, r: Report, lines: Line[]) {
  const failed = lines.find((l) => l.result === "fail");
  const passed = lines.filter((l) => l.result === "pass").length;
  tag.hidden = r.verdict === "exempt";
  if (failed) {
    tag.dataset.tone = "stop";
    tag.textContent = failed.name;
    tag.removeAttribute("aria-label");
  } else {
    tag.dataset.tone = passed === lines.length ? "done" : "part";
    tag.textContent = `${passed} von ${lines.length}`;
    tag.setAttribute("aria-label", `${passed} von ${lines.length} Prüfungen stimmen`);
  }
}

function code(text: string): HTMLElement {
  const el = document.createElement("code");
  el.textContent = text;
  return el;
}

// short digest: sha256 9f2c…e41a
function digest(d: string | undefined): HTMLElement {
  const hex = (d ?? "").replace(/^sha256:/i, "");
  return code(hex.length > 12 ? `sha256 ${hex.slice(0, 4)}…${hex.slice(-4)}` : `sha256 ${hex}`);
}

function list(items: (string | Node)[][]): (string | Node)[] {
  const out: (string | Node)[] = [];
  items.forEach((item, i) => {
    if (i > 0) out.push(i === items.length - 1 ? " und " : ", ");
    out.push(...item);
  });
  return out;
}

function linesOf(r: Report, app: Status): Line[] {
  if (r.verdict === "exempt") {
    return [{name: "Prüfung", result: "skip", detail: ["für diese App abgeschaltet"]}];
  }
  const checks = r.checks ?? [];
  const lines: Line[] = [checksumLine(r, checks)];
  const origin = originLine(r, checks);
  if (origin) lines.push(origin);
  const sender = checks.find((c) => c.id === "sender");
  if (sender) lines.push(senderLine(r, sender, app));
  return lines;
}

function checksumLine(r: Report, checks: Check[]): Line {
  const name = "Prüfsumme";
  const by = (id: string) => checks.find((c) => c.id === id);
  const forge = by("digest.forge"), file = by("digest.checksums"), synced = by("digest.synced");
  const policy = checks.find((c) => c.id === "policy" && c.code === "checksum_required");
  if (forge?.result === "fail") {
    return {name, result: "fail", detail: [digest(r.digest), ` weicht ab von ${forge.source}, dort steht `, digest(forge.want)]};
  }
  if (file?.result === "fail") {
    return {name, result: "fail", detail: file.code === "ambiguous" ?
      [code(file.source!), " nennt die Datei mehrfach mit verschiedenen Prüfsummen"] :
      [digest(r.digest), " weicht ab vom Eintrag in ", code(file.source!)]};
  }
  if (synced?.result === "fail") {
    return {name, result: "fail", detail: synced.code === "contested" ?
      ["deine Macs haben für diese Version verschiedene Pakete gesehen"] :
      [digest(r.digest), " weicht ab von dem, was ein anderer Mac für diese Version geladen hat"]};
  }
  if (policy?.result === "fail") {
    return {name, result: "fail", detail: ["das Release veröffentlicht keine, die strenge Kontrolle verlangt eine"]};
  }
  const matched: (string | Node)[][] = [];
  if (forge?.result === "pass") matched.push([forge.source ?? ""]);
  if (file?.result === "pass") matched.push([code(file.source!)]);
  if (synced?.result === "pass" && synced.code !== "accepted") matched.push(["deiner Liste"]);
  if (matched.length) return {name, result: "pass", detail: [digest(r.digest), " stimmt mit ", ...list(matched)]};
  if (synced?.code === "accepted") {
    return {name, result: "pass", detail: [digest(r.digest), " von dir angenommen, ein anderer Mac hat ein anderes Paket gesehen"]};
  }
  return {name, result: "skip", detail: [digest(r.digest), ", nichts zum Vergleichen: keine veröffentlichte Prüfsumme, und keiner deiner Macs hat diese Version geladen"]};
}

// »Herkunft«: the build attestation. Like the mockup, the line is left out
// when the forge simply has none, so most apps keep two lines; it shows
// whenever an attestation was checked, could not be checked, or was required.
function originLine(r: Report, checks: Check[]): Line | null {
  const name = "Herkunft";
  const c = checks.find((x) => x.id === "attestation");
  const required = checks.find((x) => x.id === "policy" && (x.code === "attestation_required" || x.code === "workflow_required"));
  const file = (path: string | undefined) => code((path ?? "").split("/").pop() ?? "");
  if (c?.result === "pass" && r.origin) {
    const o = r.origin;
    const ref = o.ref.replace(/^refs\/(heads|tags)\//, "");
    return {name, result: "pass", detail: ["gebaut von ", file(o.workflow), " aus ", code(ref), " ", code(`@ ${o.commit.slice(0, 7)}`)]};
  }
  if (c?.result === "fail") {
    switch (c.code) {
      case "repository":
        return {name, result: "fail", detail: ["gebaut in ", code(c.got ?? ""), ", erwartet ", code(c.want ?? "")]};
      case "workflow":
        return {name, result: "fail", detail: ["gebaut von ", file(c.got), ", verlangt ist ", file(c.want)]};
    }
    return {name, result: "fail", detail: ["die Signatur des Herkunftsnachweises ließ sich nicht bestätigen"]};
  }
  if (required) {
    return {name, result: "fail", detail: required.code === "workflow_required" ?
      ["kein bestätigter Herkunftsnachweis, verlangt ist ", file(required.want)] :
      ["kein bestätigter Herkunftsnachweis, die strenge Kontrolle verlangt einen"]};
  }
  switch (c?.code) {
    case "unreadable":
      return {name, result: "skip", detail: [`${c.source} hat den Herkunftsnachweis nicht herausgegeben`]};
    case "trust":
      return {name, result: "skip", detail: ["nicht prüfbar, die Sigstore-Vertrauensanker waren nicht erreichbar"]};
  }
  return null; // none or unsupported: nothing to say, as in the mockup
}

function senderLine(r: Report, c: Check, app: Status): Line {
  const name = "Absender";
  const who = (s: Sender | null | undefined) => {
    const out: (string | Node)[] = [code(s?.bundleId || "ohne Bundle-ID")];
    if (s?.teamId) out.push(" · Team ", code(s.teamId));
    return out;
  };
  switch (c.code) {
    case "same":
      return {name, result: "pass", detail: [...who(r.sender), ", wie bisher"]};
    case "accepted":
      return {name, result: "pass", detail: [...who(r.after), ", von dir angenommen, bisher ", ...who(r.before)]};
    case "changed": {
      const war = document.createElement("span");
      war.className = "changed";
      war.textContent = "war ";
      const ist = document.createElement("span");
      ist.className = "changed";
      ist.textContent = ", ist jetzt ";
      return {name, result: "fail", detail: [war, ...who(r.before), ist, ...who(r.after)]};
    }
    case "first":
      return {name, result: "skip", detail: [...who(r.sender), `, erste Lieferung von ${app.name}, gilt ab jetzt als bekannt`]};
  }
  return {name, result: "skip", detail: ["das Paket nennt keine Bundle-ID"]};
}

function lineElement(l: Line): HTMLLIElement {
  const li = clone<HTMLLIElement>(lineTemplate);
  li.dataset.result = l.result;
  $("use", li).setAttribute("href", icon[l.result]);
  const name = $(".customs-name", li);
  name.append(l.name);
  $(".visually-hidden", name).textContent = ` ${said[l.result]}: `;
  name.append($(".visually-hidden", name)); // read after the name
  $(".customs-detail", li).append(...l.detail);
  return li;
}

function showSender(dd: HTMLElement, s: Sender | null | undefined, other: Sender | null | undefined) {
  const bundle = $<HTMLElement>(".bundle", dd);
  const team = $<HTMLElement>(".team", dd);
  bundle.textContent = s?.bundleId || "ohne Bundle-ID";
  team.replaceChildren(s?.teamId ? "Team " : "ohne Team-ID");
  if (s?.teamId) team.append(code(s.teamId));
  // only what differs is marked, and only on the new side
  const after = dd.dataset.sender === "after";
  bundle.toggleAttribute("data-changed", after && (s?.bundleId ?? "").toLowerCase() !== (other?.bundleId ?? "").toLowerCase());
  team.toggleAttribute("data-changed", after && (s?.teamId ?? "") !== (other?.teamId ?? ""));
}

async function review(app: Status) {
  const r = app.customs;
  if (r?.verdict !== "ask") return;
  $("#sender-title", senderDialog).textContent = `${app.name} ${r.version} kommt von einem anderen Absender`;
  showSender($('[data-sender="before"]', senderDialog), r.before, r.after);
  showSender($('[data-sender="after"]', senderDialog), r.after, r.before);
  if (await ask(senderDialog) !== "accept") return;
  await install(app.repo, {accept: true});
}
