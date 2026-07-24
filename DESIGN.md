# Design system

The binding design system for this repo. **Every agent making a frontend or UI change reads this
file first and conforms to it.** It is the source of truth for look and feel: when a request
conflicts with a decision here, flag the conflict instead of silently diverging. Evolve the system
by re-running the interview and editing this file in a PR, never by forking choices per feature.

If `/impeccable` is installed it auto-loads this file from the repo root - keep it here, named
`DESIGN.md`, so that path keeps working.

Derived from the dashboard requirements in `docs/architecture/frontend.md`; no human interview
was run (this repo operates autonomously, see VISION.md). Re-run the design interview to revise.

## Register

Product - design serves a data-dense operations dashboard. Identity stays out of the way;
the agent's work is the spectacle, not the chrome around it.

## Voice & tone

- Personality: precise, calm, evidentiary
- Anti-references: hacker-terminal green cosplay; crypto-trading dashboards; the generic
  dark-blue observability SaaS look (Grafana/Datadog clones); marketing gradient glow

## Color

- Strategy: restrained (tinted neutrals + one accent <=10% of surface)
- Space: OKLCH. Reduce chroma near lightness 0/100. Never `#000` or `#fff`; every neutral is
  tinted toward the brand hue (warm clay, hue ~60, chroma ~0.008).
- Roles:
  - Background: `oklch(0.16 0.008 60)`
  - Surface: `oklch(0.20 0.008 60)`
  - Text / muted text: `oklch(0.93 0.005 60)` / `oklch(0.68 0.008 60)`
  - Border: `oklch(0.30 0.008 60)`
  - Accent: `oklch(0.70 0.14 55)` (ember) - interactive elements, focus rings, links,
    primary buttons, the running-task indicator
- Semantic: success `oklch(0.72 0.17 150)` / warning `oklch(0.82 0.14 95)` / danger
  `oklch(0.63 0.20 25)`
- Theme: dark. Scene sentence: "A developer keeps Agent Trail on the second monitor beside
  their own terminal in a dim evening room, glancing over for state changes and reading logs
  closely only when something fails." Light theme is out of scope until the MVP ships; build
  with tokens so it stays possible.

## Typography

- Heading / display font: Inter (Google Fonts), weight contrast 600/450
- Body font: Inter
- Mono font: JetBrains Mono - every Git or shell artifact renders mono: logs, commands, SHAs,
  branch names, file paths, diff stats
- Scale + ratio: 13 / 16 / 20 / 25 / 31 px (ratio 1.25)
- Body measure: 65-75ch for prose surfaces (evidence reports, issue text); data tables and
  timelines are exempt
- Hierarchy comes from scale + weight contrast, not color alone.

## Layout & spacing

- Spacing scale: 4 / 8 / 12 / 16 / 24 / 32 / 48
- Rhythm: dense where data lives (4/8 inside tables, timelines, log rows), generous between
  sections (24/32). Uniform padding everywhere is monotony.
- Container policy: full-width app shell with a fixed sidebar; only prose surfaces (evidence
  report, task instructions) get a measure-limited container
- Cards: only for genuinely self-contained units (a validation result, a runner). Timeline
  entries and table rows are rows, not cards. Nested cards are always wrong.
- Breakpoints: desktop-first; primary target 1280+, functional at 1024. Mobile is a
  spec non-goal.

## Elevation

Flat: 1px borders + surface lightness steps. No shadows except floating overlays (menus,
dialogs), which get a border plus a single soft shadow. Don't mix strategies.

## Motion

- Easing: ease-out with an exponential curve (quart / quint / expo). No bounce, no elastic.
- Never animate CSS layout properties.
- Durations: 120ms micro (hover, toggle), 240ms transitions (drawer, panel).
- Live updates: new timeline rows appear with a brief background-tint fade (240ms), never a
  slide or bounce. Follow-mode log streams do not animate at all.

## Components

Canonical patterns as they stabilize. Seed rules:

- Status badges: task states map to fixed colors - queued muted, running accent, completed
  success, failed/timed-out danger, cancelled muted, awaiting-review warning.
- Trust distinction (spec requirement): a **filled** badge marks a platform-verified fact
  (trusted validation); an **outlined** badge marks an agent claim. Never blur the two.
- Logs: mono 13px, virtualized, follow mode pinned to bottom; redaction markers are visible
  (muted background + "redacted" tag), never silent gaps.
- Empty states: one sentence + one action. No illustration library.
- Destructive actions (cancel task): inline confirmation, not a modal, unless data loss is
  irreversible.

## UI verification (mandatory)

Code reading is never enough to ship UI. **Every change that touches UI gets an in-browser
audit before merge, every single time** - run the app and drive each changed flow with
Playwright (or the repo's browser runner if one is committed), screenshot every changed
screen and state (default viewports 1280 and 1024), and audit the shots against this
checklist:

- Structure: alignment to the spacing scale, no overlap, no clipped text, no overflow,
  consistent gutters
- Balance: visual weight distributed deliberately - no lopsided screens, no orphaned
  controls, hierarchy readable at a squint
- System conformance: colors, type scale, spacing rhythm, elevation, and motion match this
  file; trusted-vs-claimed badges correct
- States: empty, loading, error, and long-content states all shot, not just the happy path
- Best practices: focus visible, contrast sufficient, hit targets adequate, no layout shift
  while streaming

Fix every defect found and re-shoot until the audit is clean - "renders without errors" is
not the bar; balanced and best-practice is. Attach the final screenshots to the PR body as
evidence. `/ui-audit` runs the full-sweep version of this protocol; `/impeccable critique`
is the per-screen deep pass.

## Absolute bans (match-and-refuse)

If you are about to write one of these, restructure the element instead.

- Side-stripe borders (`border-left`/`border-right` > 1px as a colored accent).
- Gradient text (`background-clip: text` + gradient background).
- Glassmorphism as a default.
- The hero-metric template (big number, small label, supporting stats, gradient accent).
- Identical card grids (same-sized icon + heading + text, repeated).
- Modal as the first thought - exhaust inline / progressive alternatives.
- Em dashes in UI copy (and `--`).

## The AI slop test

Ship nothing that reads as "AI made that". The first-order reflex for this domain is
"observability -> dark blue + terminal green"; this system counters with warm clay neutrals
and an ember accent. If a screen would pass as a Grafana clone with the logo swapped,
rework it before shipping.
