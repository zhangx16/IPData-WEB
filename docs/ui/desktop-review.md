# Desktop UI review — v0.4.0

The user invoked ui-design-iteration from https://git.nite07.com/nite/skills.git and explicitly limited optimisation to desktop computers. It was installed using the requested skills CLI; the installed SKILL.md was read and applied. The running IPData-WEB v0.3.0 page is the sole visual reference.

## Work and source

A live DOM/style capture preceded changes. The existing blue/white palette, system font, flat surfaces and 12px/6px radii were retained. Changes were split into hierarchy and density rounds, with before/after screenshots and a design contract. Configuration is progressively disclosed, real task counts are grouped above results, and VLESS latency/download columns lead the table. The node-export disclosure uses the existing form/details component language.

[Static offline draft](../../design/desktop/index.html) · [Design contract](../../design/desktop/design-contract.md). The draft's dataset is the existing three-candidate recorded test, not new performance evidence. Its lifecycle and exports are approximations; the production Go application has separate live data and export APIs.

## Review scope

Desktop widths: 1024, 1280, 1440 and 1920 CSS pixels. Completed/configuration/export/empty/running/paused/error states were reviewed at each width: 28 combinations. The user's desktop-only scope overrides the skill's default phone/tablet widths. The reference's 15px body and 13px table text were retained as an explicit dense-tool exception to the default 16px heuristic; contrast and keyboard checks were not waived.

- Text contrast failures: 0; unknown or approximate contrast readings: 0.
- Document overflow: 0 at reviewed widths. The result table deliberately has its own horizontal/vertical scroll container.
- Gradients, at-rest shadows and animated components: 0.
- Two radii: 6px and 12px. At most five type sizes and two type families across states.
- Visible 2px focus rings, keyboard-open/export, Escape-close/focus-return, row selection and clear-selection checked.
- Production polling no longer replaces unchanged rows; focused row controls survive polling, and a newly rendered row restores its corresponding focus target.
- Offline draft opens from file without HTTP requests. Review tools, DRAFT_KIT configuration, draft-kit.js, selection snapshots and slider attributes are absent from the handoff and production assets.

The raw per-state audit, before/after screenshots, contact sheets and browser traces are kept under ignored test-artifacts. All 28 captured states were visually reviewed on desktop contact sheets, with full-size result/export views checked separately. No mobile/tablet redesign was performed.
