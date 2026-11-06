# IPData-WEB desktop design contract

## Source and scope

The reference is the running IPData-WEB v0.3.0 product, captured in `test-artifacts/ui-before/reference-dom.html` at 1280/1440/1920px. Existing HTML/CSS and the real three-candidate completed task are the source. No external template, palette, illustration or marketing proof is introduced. The user explicitly requested this skill to improve the current UI and then limited scope to desktop computers.

The UI work and the separate backend/production integration are both authorized by the user's requests. The static draft is reviewed independently before its markup/styles are integrated into the existing Go application; no framework is introduced.

## Argument and surfaces

- Task toolbar: choose the saved test and operate its lifecycle; copied from the existing task selector/pause/resume controls.
- Overview: see real completed/total/hit/download counts; a compact data strip composed from the existing task status.
- Configuration: create a test with target input, template and advanced parameters; existing controls grouped by when they are needed.
- Results: compare actual VLESS latency and download speed, select qualified rows, export; existing table and evidence view extended for the authorized export feature.
- Node export: choose saved/custom VLESS template and export current filter/all successful downloads/selected results. An inline disclosure composed from the product's existing details/form surfaces.
- Evidence/empty/error/running/paused: reachable from the page or draft runtime state controls. Static lifecycle states are interaction examples, not new measurements.

## Standing decisions

- Desktop only: reviewed at 1024, 1280, 1440 and 1920px; mobile/tablet optimisation explicitly excluded by the user. This overrides the skill's default phone/tablet audit widths, not accessibility checks.
- Preserve blue/white reference palette, system type, flat panels, a single primary accent; no gradient or ornamental imagery.
- Two radii (12px panel, 6px controls), no at-rest shadows; desktop tabular text can remain 13px and body 15px as in the reference (explicit override of the default 16px running-text heuristic for a compact tool). Text contrast/focus/keyboard targets still must pass.
- Intentional horizontal scroll may exist inside the results table; the document itself must not overflow at supported desktop widths.
- No review kit, selection snapshots or mocked data wiring may ship in the production application.

## Token source and rounds

Intake maps the product's existing full colours to `--primary`, `--ink-color`, `--surface-color`, `--line-color`; hue/lightness sliders are intentionally unbound rather than guessing a new palette. Spacing starts at 16px, body at 15px, radius at 12px, border at 1px, motion at 0ms.

Round 1 changes hierarchy only: place task operations together, expose real counts, group basic vs advanced configuration, prioritise VLESS/download columns, group result/export actions. Reference palette is unchanged.
Round 2 (applied) changes density only: `--room` 1.375→1, app bar 72→64px, table vertical padding 12→8px, and task-picker label becomes inline. reduce competing whitespace, give target/template fields a shared row, collapse configuration after task selection/start, make data rows and toolbars compact.
Round 3 covers reachable states, contrast, focus, keyboard and desktop sizing. No new theme is introduced.

## Anchors

`app/header`, `tasks/toolbar`, `overview`, `configuration`, `configuration/form`, `configuration/targets`, `configuration/template`, `configuration/advanced`, `configuration/speed`, `configuration/actions`, `results`, `results/filters`, `results/actions`, `results/selection`, `results/table`, `results/rows`, `results/export`, `results/evidence`, `app/footer`.

## Handoff (updated after audit)

Runtime: `draft-runtime.js`, styles: `app.css`, entry: `index.html`; all local, no build step. The draft approximates lifecycle and exports without starting scans or returning credentials. Production functionality is supplied separately by Go APIs and `app.js`.
Reviewed desktop widths: 1024/1280/1440/1920px, seven states each (28 combinations). Contrast failures, unknown/approximate contrast checks, running transitions, document overflow and page errors: zero. Keyboard export-open/Esc-close/focus-return, selection and visible 2px focus ring checked. Review tooling and snapshots removed from handoff. Screenshots and raw audits stay in ignored test-artifacts. The design contract, final static artifact and audit summary are retained.
