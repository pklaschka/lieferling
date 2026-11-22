---
name: Lieferling
description: A dispatch list for your own desktop apps, set like a parcel label.
colors:
  kraft: "light-dark(oklch(76% 0.07 70), oklch(37% 0.05 60))"
  kraft-ink: "light-dark(oklch(21% 0.03 55), oklch(95% 0.015 75))"
  kraft-line: "light-dark(oklch(64% 0.07 65), oklch(46% 0.05 60))"
  label-stock: "light-dark(oklch(99.2% 0 0), oklch(19% 0.006 60))"
  well: "light-dark(oklch(96.2% 0.002 60), oklch(23.5% 0.006 60))"
  ink: "light-dark(oklch(18% 0.01 60), oklch(95% 0.004 75))"
  ink-2: "light-dark(oklch(43% 0.01 60), oklch(74% 0.008 70))"
  cut-line: "light-dark(oklch(80% 0.006 60), oklch(36% 0.008 60))"
  express: "light-dark(oklch(53% 0.2 27), oklch(70% 0.17 27))"
  express-ink: "light-dark(oklch(99% 0 0), oklch(17% 0.04 27))"
  express-soft: "light-dark(oklch(95.5% 0.03 27), oklch(29% 0.07 27))"
  delivered: "light-dark(oklch(44% 0.09 152), oklch(76% 0.11 152))"
  pending: "light-dark(oklch(82% 0.004 60), oklch(38% 0.006 60))"
  focus: "{colors.ink}"
  edge: "color-mix(in oklch, {colors.ink-2} 70%, {colors.label-stock})"
typography:
  headline:
    fontFamily: "'Archivo Variable', system-ui, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 800
    letterSpacing: "0.02em"
  title:
    fontFamily: "'Archivo Variable', system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 700
  tracking-number:
    fontFamily: "ui-monospace, 'SF Mono', Menlo, monospace"
    fontSize: "1rem"
    fontWeight: 600
    letterSpacing: "0.02em"
  body:
    fontFamily: "system-ui, -apple-system, sans-serif"
    fontSize: "0.875rem"
    lineHeight: 1.45
  label:
    fontFamily: "system-ui, -apple-system, sans-serif"
    fontSize: "0.75rem"
rounded:
  tag: "3px"
  item: "2px"
  stamp: "7px"
  control: "4px"
  sheet: "8px"
spacing:
  row: "1rem"
  gutter: "1rem"
  gap: "0.5rem"
components:
  button:
    backgroundColor: "{colors.label-stock}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0.25rem 0.8rem"
  button-ink:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.label-stock}"
    rounded: "{rounded.control}"
  button-express:
    backgroundColor: "{colors.express}"
    textColor: "{colors.express-ink}"
    rounded: "{rounded.control}"
  button-on-kraft:
    backgroundColor: "transparent"
    textColor: "{colors.kraft-ink}"
    rounded: "{rounded.control}"
---

# Lieferling design system

## Overview

**Creative North Star: the parcel label.** Every app on the list is a shipment. The window is a dispatch list: a kraft-paper title bar, white label stock below, black ink, version numbers set like tracking numbers, and a three-stop tracking strip (Erschienen, Geladen, Installiert) that shows where each app stands. It is a macOS utility in Operate mode: calm when nothing is due, unmistakable when something is.

Mood: practical, printed, a little warm. Not a store front, not a dashboard, no cards, no gradients, no glass.

## Colors

### Primary
- **Kraft** (`kraft`): the title bar only, the paper the label sits on. Its own ink (`kraft-ink`) and rule (`kraft-line`) belong to it. The bar is real paper, not a flat fill: three noise layers drawn by the browser from inline SVG (`--kraft-mottle` a cloudy tone, `--kraft-grain` fine specks, `--kraft-fibres` a few short light fibres), retinted for dark mode (pale grain and dim fibres on dark kraft), with `color-interpolation-filters='sRGB'` so the tint does not depend on the engine. A 2px `kraft-line` edge closes the bar like the edge of a carton. Kept quiet enough that the bar text reads as if printed on it.

### Neutral
- **Label stock** (`label-stock`): the window ground. **Well** (`well`): pressed and hovered surfaces.
- **Ink** (`ink`) for text and the primary "Installieren" button, **Ink 2** (`ink-2`) for secondary text, **Cut line** (`cut-line`) for the dashed separators between parcels.

### Status
- **Express red** (`express`): appears only while an update is waiting or something failed. The new version number, the "Erschienen" stop and the "Aktualisieren" button turn red together.
- **Delivered green** (`delivered`): a completed tracking strip on an app that is current.
- **Pending** (`pending`): stops not yet reached.

### Controls
- **Edge** (`edge`): the 1px frame of buttons, the text field and the tick box, a step darker than the cut line so a control holds 3:1 against the stock. Cut lines stay `cut-line`.
- **Focus** is printed in ink (kraft-ink on the bar), never a blue: a 2px outline 2px off buttons and links; the text field instead thickens its own frame to ink (express while invalid), so there is never a box around a box.

### Named Rules
- **Red means action.** Express red is never decoration. If no update waits and nothing failed, there is no red on screen.
- **Kraft stays up top.** Kraft is the bar, never a row, button fill or highlight.

## Typography

Three voices. **Archivo** (variable, self-hosted from `@fontsource-variable/archivo`, OFL) is the printed voice of the label: the sender's name wide and in capitals, app names and dialog titles at normal width, field labels narrow and in capitals. System UI for running text and controls, so the window still sits next to macOS's own utilities. The mono stack for everything that is an identifier (versions, paths).

### Hierarchy
- **Headline** (Archivo 1.25rem, 800, width 125%, capitals, +0.02em): the window name in the bar, like a sender's name printed on a box.
- **Title** (Archivo 1rem, 750, width 110%): app name; dialog titles and the empty state's heading at normal width, 700.
- **Stamp initial** (Archivo, 800, width 125%): the letter in an empty stamp field, stamped in the wordmark's voice. The bar's summary is Archivo 500, so the bar speaks in one voice.
- **Tracking number** (mono 1rem, 600, +0.02em): installed → newest version.
- **Body** (0.875rem / 1.45): everything else.
- **Label** (0.75rem): repository, status text, footer.
- **Field label** (Archivo 0.6875rem, 600, width 75%, capitals, +0.05em): the route stops; the "Vorab" tag the same at 700.

### Named Rules
- **Numbers are mono.** A version or path is always set in `--mono`; prose never is.
- **Archivo labels, system reads.** Archivo names things (the app, an app, a dialog, a field); buttons are labels too and use it at weight 650; sentences and inputs stay in the system face.

## Layout

One column of parcels on a four-row window grid: bar, add form, scrolling list, footer. Each parcel is a subgrid row with four columns: who (stamp, name, repo), shipment (number and route), action, more menu. Parcels are separated by dashed cut lines, not boxes. Below 640px the shipment moves to a second row, indented to line up with the name, and the bar summary hides. The bar leaves 5.5rem on the left for the macOS traffic lights and is the window's drag region.

## Elevation & Depth

Flat. The label lies on the table. Only things that float above the window cast a shadow: the more menu (popover) and the dialogs.

## Shapes

Small, cut-paper radii: 4px for controls, 2px for menu items, 3px for the "Vorab" tag and the tick box, 7px for the empty stamp field, 8px for menus' and dialogs' sheets. Route stops are 9px dots.

## Components

### Buttons
Outlined on label stock by default; a current row uses it for "Öffnen", which starts the installed app (hidden on Lieferling's own row). Variants: **ink** (filled black, the one action on a row that is not current), **express** (filled red, "Aktualisieren" and "Alle aktualisieren"), **on-kraft** (outlined, in the bar), **danger** (red text, red-soft on hover), **icon-only** (borderless 32px square, the more button).

Every variant sets only `--bg`, `--fg` and `--bd`, and states change only those, so no state can leak one variant's look into another. Hover darkens the edge to ink (fills darken by mixing with ink); a press shades and drops the button 1px like a stamp; an open menu keeps its trigger in the hover look; disabled outlined buttons fade and turn their edge dashed, like the loading placeholders.

### Inputs
One text field with the hint in the placeholder, a checkbox for prerelease drawn as an ink tick box (WebKit before Safari 26.2 ignores `accent-color` and would show system blue), a placeholder that ends in an ellipsis rather than a cut letter, an error line below that reserves its height so the form does not jump.

### Menu
A popover under the more button with "Öffnen" (disabled while the app is not installed here) above a rule, a checkable item (Vorabversionen verfolgen), a link to the release page (named for its forge: GitHub, Codeberg or the host) and, after a rule, the destructive item.
Pointer hover shades an item with `well`; the keyboard's item is printed solid (ink, or express for the destructive item). Items leave room for a tick only in menus that have one. In dark mode the sheet is `well`, a step lighter than the page. Menus fade in from 3px above.

### Command line dialog
Opened from the app menu ("Kommandozeilen-Tool installieren …") or the quiet text button at the end of the footer. One dialog with four states set by `data-state`: `missing` (Abbrechen, Installieren), `other` (Behalten, Ersetzen), `linked` (Entfernen on the left, Fertig) and `unavailable` (Schließen). Below the note a dashed slip shows the link like an address: `/usr/local/bin/lieferling` on the first line, an arrow and the target in `ink-2` on the second, breaking only after a slash; once linked a delivered-green tick stands before it. The work happens with the dialog open because macOS asks for the password on top; the main button says "Wird installiert …" meanwhile, a line asks for the password, and Abbrechen stays live and stops the wait (after two minutes Lieferling gives up by itself). Closing the password dialog changes nothing and shows no error. When installing fails, the `sudo` command that does the same appears below the error in a `well` box with a Kopieren button.

### Catalog menu and import
"Katalog" sits in the bar beside "Prüfen", an on-kraft button with a chevron that opens the same kind of menu as a row: "Aus Datei importieren …", "Von Adresse importieren …", a rule, "Liste exportieren …". The Ablage menu of the menu bar has the same three items (⌘O, ⇧⌘O, ⌘E), and the empty state offers the two imports as outlined buttons.

Importing never acts at once. A wider dialog shows the catalog as a packing list: the source file or address in mono under the title, then one line per entry in a `well` box with dashed cut lines, new apps first with a checked box (and the "Vorab" tag), then apps already on the list (disabled, checked, "Schon auf der Liste"), then unreadable entries (disabled, their reason in express red below). The ink button counts what it adds ("3 Apps hinzufügen"); with nothing new it disappears and Abbrechen becomes Schließen. The address dialog keeps its error line's height so the buttons do not jump. Results ("3 Apps aus dem Katalog hinzugefügt", "… als Katalog gesichert") show for five seconds in the bar's summary, which on narrow windows appears just for them.

### Stamp
Where a parcel label carries its postage, each parcel carries the app's own icon, 36px, left of the name. It comes from the installed .app bundle, or else from the icon file the repository builds the app from (such as `build/icon.png`); Lieferling keeps it in its cache and asks GitHub again after a week. Until it arrives, and for apps without one, the stamp field stays empty: a dashed `cut-line` frame on the macOS icon grid (inset a tenth on each side, so empty and filled stamps line up) with the app's initial in `ink-2`. A loaded icon fades in over the frame. No ring, shadow or tint is added to real icons; they bring their own shape.

### Customs note (Zollvermerk)
Drawn like the delivery note of #29. One printed line under the parcel, flush with the row: »ZOLL« in the field-label voice in ink, the verdict in `ink-2`, a stamped tag and a chevron that points up while open. The tag counts the lines that matched (»2 von 2« in delivered green, fewer in `ink-2`) or names the line that stopped the package (»Absender«, »Prüfsumme« in express red). Unfolded, a `well` box with a `cut` border holds one line per check, separated by dashed cuts: icon (green tick, red alert, `ink-2` dots for not checkable), the check's name in semibold ink, then what was compared in `ink-2` with digests and bundle IDs in mono ink. The three digest checks read as one line, »Prüfsumme«. The lines stand in the mockup's order: Prüfsumme, Herkunft, Absender. »Herkunft« (the build attestation) reads »gebaut von `release.yml` aus `main` `@ 4c1d9e0`« and is left out when the forge simply has none, so most apps keep two lines; it appears whenever an attestation was checked, could not be checked or was required. »Apple-Signatur« comes last and reports what codesign and spctl say and what happened to the quarantine flag (»nicht signiert, Quarantäne wurde entfernt«); the tag does not count it, which is how four lines read »3 von 3«, so it keeps its dots even when the signature is valid (ticks equal the count), but a broken signature still names the tag, shortened to »Signatur«. The policy of one app is a dialog from the row menu (»Zollkontrolle …«) with four round ink radios that may wrap: »Wie alle Apps« (naming the current global policy), Standard, Streng, Aus, each with its name in semibold ink; the chosen radio has the focus and Enter takes it; under Streng a red note says when the forge cannot meet it. The policy for all apps is a radio submenu in the app menu, not a window. A footer line under a dashed cut explains a stop; for a sender change it carries the underlined »Trotzdem installieren …«, which opens a dialog with »Bisher« and »Jetzt« where only what differs is red, and the safe button has the focus. A held package, a sender change and a dispute between Macs stay open (`aria-disabled` summary, out of the tab order, no chevron). No line may read as if Apple checked anything.

### Tracking strip (signature)
An ordered list of three stops drawn as dots joined by legs. States come from `data-state` on the parcel: `current` (all green), `missing` (released in ink, rest pending), `update` (released in red), `failed` (strip hidden, "Kein Release erreichbar"). While installing, `data-busy` takes over: the first leg fills with download progress (`--progress`), the "Geladen" stop pulses, then the second leg half-fills during install. Motion stops under reduced motion.

### Loading parcels
Until the first list arrives, blank parcels stand on the same subgrid as the real rows and have the same height, as many as the list had last time (kept per Mac in local storage, 1 to 6, 3 the first time). The stamp, name, repository, number and stop labels are slips (`pending` mixed into label stock, 3px radius), the action is a dashed outline the size of a button. The one moving thing: a short `ink-2` segment travels along each blank tracking strip, rows a little out of step; under reduced motion it stands still in the middle. The bar reads "Sendungen werden abgerufen …". The placeholders stay at least 700 ms so a fast answer does not flicker, then the rows fade in from 3px below. Refreshing later keeps the current rows instead.

## Do's and Don'ts

### Do:
- Let the row state drive color through `data-state`; never hand-color a single element.
- Keep one action per row: filled when something is due, outlined "Öffnen" when the app is current.
- Write German, short, in full words ("Wird geladen …", not icons alone).

### Don't:
- Add cards, badges or counters to rows; the tracking strip already carries the state.
- Use express red or delivered green outside their meaning.
- Use kraft below the bar.
