---
name: styk-ui-workflow
description: "Design, implement and verify user-visible STYK UI changes: layout, CSS, components, interactions, navigation, copy, responsive behavior and accessibility. Coordinate the official frontend-design and Anthropic Design skills for the affected scope. Use even for small visual fixes; exclude backend-only work and workflow documentation edits."
---

# STYK UI workflow

Make the requested user task clear, usable and consistent with STYK, then verify
the rendered result. Use this workflow automatically for UI work; the user does
not need to name the design skills. It supports the task without expanding its
scope or requiring a separate approval of an ordinary implementation plan.

## Load the relevant skills

Resolve the official plugins through the available skill catalog and read each
applicable `SKILL.md` before that phase. Reuse instructions already read in the
current context if unchanged. A skill's name in a report is not evidence of use.

| Skill | Required use |
| --- | --- |
| `frontend-design` | Every UI task: deliberate hierarchy, layout and visual choices within the existing product direction. Prefer `frontend-design:frontend-design` from the official plugin when duplicate names exist. |
| `design-system` | Every UI task: inspect and reuse the affected components, tokens, variants and states. Extend an existing pattern only when the task requires it. |
| `design-critique` | Every UI task: review the proposal against the brief and critique the rendered result for usability, hierarchy and consistency. |
| `accessibility-review` | Every UI task: review the affected content and controls. Exercise relevant keyboard behavior, semantics, focus, contrast and responsive accessibility. |
| `ux-copy` | When adding or changing visible labels, instructions, errors, confirmations, empty states or action names. |
| `user-research` | When investigating an unclear user problem or planning usability research. Use known context first; ordinary styling fixes do not require interviews. |
| `research-synthesis` | When supplied or authorized research evidence needs analysis, such as feedback, support tickets, interviews or usability notes. |
| `design-handoff` | When the deliverable includes a design specification, implementation by another developer, or a new shared pattern whose interaction contract needs documenting. |

Briefly name the selected skills and scope in the first progress update. Consider
all rows, but do not run irrelevant research or produce eight separate reports.
For a small change, a focused application of the four required skills suffices.

Plugin identities, portable setup and missing-dependency handling are in
[references/skill-dependencies.md](references/skill-dependencies.md). Read it when
a dependency cannot be resolved or when preparing another developer's machine.
Use available browser tools; Figma and other connectors are optional. This skill
does not authorize publishing documents, creating tickets or messaging others.

## Ground the change

- Read the current `AGENTS.md`, relevant product and frontend styling
  documentation. Inspect the
  working tree and preserve unrelated changes. Find the owning components,
  stylesheet order, existing states and API contracts before editing.
- State the user task, the current friction and observable acceptance criteria.
  For a tiny fix, one sentence is enough. Use the user's reference, correction
  and existing context before asking for information already available.
- Inspect the current rendered UI when available. For a visual bug, capture the
  affected component and inspect computed styles before choosing a CSS fix.
- Reuse STYK's established typography, spacing, controls and terminology.
  Approved references and product constraints take precedence over a plugin's
  aesthetic defaults. An operational screen needs readable data and clear
  actions; a request to improve it does not imply a new brand or landing page.
- For a substantial redesign, compare meaningfully different information or
  interaction layouts on a representative screen before spreading the chosen
  pattern. A targeted fix does not need alternative mockups or a new token set.

Use real content where authorized, or representative fixtures with long Russian
names, identifiers and realistic data density. Label assumptions and distinguish
observations from hypotheses. Never invent interviews, quotes, usage metrics or
research findings to justify a choice.

## Implement the complete affected interaction

- Preserve existing permissions and business semantics. Do not invent backend
  capabilities, successful saves or unavailable data. Treat unknown or stale
  values honestly. Keep important identifiers discoverable without letting them
  overwhelm the primary user task.
- Reuse components and styles before adding variants. Follow the styling rules
  for CSS ownership, scoped selectors and cascade order. Correct the winning
  conflicting rule rather than appending another override.
- Account for applicable loading, empty, error, disabled, selected and success
  states. Include validation, long content, partial data and permission states
  where the changed flow exposes them. Avoid silently enabling actions during
  a pending request or showing success before the existing contract confirms it.
- Use semantic controls with accessible names and visible focus. Preserve
  keyboard operation, dialog focus entry/return and Escape behavior. Changes to
  motion must respect reduced-motion preferences. Use color with another cue
  when conveying a state.
- Keep Russian UI copy concise and consistent with existing domain terms.
  Action labels must describe the actual result; errors and empty states should
  offer a supported next action.
- If introducing a reusable interaction, document its states and contract in
  the existing appropriate location. A small private component does not require
  a separate handoff document or a new design system.

## Verify what the user will experience

Inspect the affected route and nested controls in the running app or an isolated
presentation harness. Reuse an existing suitable preview; do not stop unrelated
processes. Fixture rendering establishes presentation behavior only. It cannot
prove a database write or provider integration.

For a new or substantially changed screen, check a normal desktop, a compact
desktop and a narrow mobile viewport (use 1440, 1366 and 390 CSS px as starting
points). For a targeted fix, check the reported viewport and the nearest affected
responsive boundary. Include a short viewport for scroll, drawer or footer bugs.
Verify the affected component at normal scale, not just a scaled full-page image.

Exercise the changed primary path and relevant states with mouse and keyboard.
Open affected menus, dialogs and accordions; inspect the first and last list rows,
pagination, scroll boundaries and focus. Check clipping, overlapping controls,
long text and unintended page overflow. Inspect computed styles or bounding
rectangles when geometry is in doubt. Check changed browser/network errors
without exposing credentials or customer data in shared artifacts.

Apply `design-critique` to the rendered result: identify the element, user impact,
severity and concrete correction. Fix critical and major findings caused by the
change, then recheck the affected state. Report pre-existing or out-of-scope
findings without silently redesigning unrelated areas. Do not substitute a
self-assigned numerical score for evidence.

Apply `accessibility-review` within the changed scope: check names/roles, focus,
keyboard operation, measured contrast and zoom/reflow as relevant. Distinguish
normative WCAG criteria and levels from usability recommendations. A screenshot,
DOM inspection or automated scan alone does not establish full compliance or
screen-reader behavior. State which manual checks were actually performed.

Browser inspection must stay within the user's data and action authorization.
Use fixtures or an isolated environment for mutations. UI verification does not
authorize Ozon/МойСклад writes, worker startup or edits to the shared Neon dataset.
If access, tools or permission prevent a check, continue independent work and
report the specific gap; do not claim visual or end-to-end acceptance.

## Select checks and report the result

Follow [testing policy](../../../docs/testing-policy.md) and run
`make check-plan` before choosing executable checks. Use the selected lanes for
the changed scope, including `make check-ui` when UI behavior/layout coverage is
selected. Keep the user's current test restrictions, reuse valid passed checks
and do not start full regression for routine UI edits or as a push prerequisite.
Add behavior tests when an interaction or a regression warrants them; do not
write brittle tests that merely match CSS values or implementation structure.

In the final response, keep the evidence proportional to the task:

- Explain the user-visible change and why it addresses the request.
- Name the applied skills; group conditional skills omitted as unnecessary with
  a short reason. Identify an unavailable skill separately.
- List the actual routes, viewports and important states inspected; link useful
  screenshots or artifacts using paths appropriate to the sharing context.
- Report executable checks as passed, failed, skipped, stopped or unrun, with
  relevant command output or counts. A build does not prove the visual result.
- Disclose remaining findings, inaccessible states and verification limitations.
  Claim completion only for work actually implemented and checked.
