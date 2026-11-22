import {AppService, type Status} from "../backend";
import {ask} from "../lib/dialog";
import {$} from "../lib/dom";
import {errorText} from "../lib/text";
import {findRow, rowAction, showRowError} from "./row";

// The customs policy of one app: an entry »Zollkontrolle …« in the row menu
// opens a dialog with four choices. The policy for all apps is in the app
// menu (main.go); the workflow pin lives on the command line.

const dialog = $<HTMLDialogElement>("#customs-policy");
const names: Record<string, string> = {standard: "Standard", strict: "Streng"};

export function setupPolicy() {
  rowAction("policy", {run: choose});
}

async function choose(app: Status) {
  $("#policy-title", dialog).textContent = `Zollkontrolle für ${app.name}`;
  let global = "standard";
  try {
    global = await AppService.Policy();
  } catch { /* the label then says Standard, which is the default */ }
  $('[data-label="default"]', dialog).replaceChildren(
    Object.assign(document.createElement("b"), {textContent: "Wie alle Apps:"}), ` zurzeit ${names[global] ?? global}`);
  const current = app.policy || "default";
  for (const input of dialog.querySelectorAll<HTMLInputElement>('input[name="policy"]')) {
    input.checked = input.value === current;
  }
  const unmet = $<HTMLElement>("#policy-unmet", dialog);
  unmet.hidden = app.forge === "GitHub";
  unmet.textContent = `${app.forge} stellt keine Herkunftsnachweise aus. Streng hält jede Version dieser App an.`;

  if (await ask(dialog) !== "ok") return;
  const chosen = $<HTMLInputElement>('input[name="policy"]:checked', dialog).value;
  if (chosen === current) return;
  try {
    await AppService.SetPolicy(app.repo, chosen);
  } catch (err) {
    const li = findRow(app.repo);
    if (li) showRowError(li, errorText(err));
  }
}
