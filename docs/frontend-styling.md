# Frontend styling and visual review

Every UI/UX task follows the mandatory project
[styk-ui-workflow](../.agents/skills/styk-ui-workflow/SKILL.md) from
[AGENTS.md](../AGENTS.md), including its required design skills. That workflow
takes precedence over this implementation guide. Approved references and the
user's corrections take precedence over aesthetic defaults in upstream skills.

## CSS ownership and loading

`web/src/main.tsx` declares the final stylesheet order. Shared resets, foundation
styles and base components load first; page styles load afterwards. Do not import
a competing page stylesheet through a component's dependency tree: importing
`App` evaluates its component imports before the entry point's global CSS.

The Products styles have these responsibilities:

| File | Owns |
| --- | --- |
| `products-workspace.css` | Base Products workspace structure |
| `products-reference.css` | Approved page appearance, rules, automation, import, detail and responsive layout |
| `products-matching-layout.css` | Matching drawers, source summary, result scrolling, pagination and inline disclosure layout |
| `products-controls.css` | Shared Products control appearance and native dropdown picker states |

Products selectors are scoped below `.reference-shell .products-workspace`.
Mobile shell elements outside that subtree use
`.reference-shell:has(.products-workspace)`. This keeps page styling from changing
unrelated screens. A component's geometry belongs in its owning file; keep its
responsive rules alongside that responsibility.

Before editing a broken component, find both its owning rules and the winning
browser declarations. Common causes are a later stylesheet, a more specific
generic descendant selector, a font shorthand that resets weight/line height, or
a flex item shrinking while its visible contents retain their height. Change the
cause; do not accumulate another set of competing overrides at the file's end.

## Spacing and typography

Use the existing Inter/system UI font stack consistently. Set explicit font size,
weight and line height for control labels and section headings. A `b` element
does not define a suitable label style by itself. Preserve the app's type hierarchy;
do not introduce a different font stack for a single switch or preset.

Prefer the established 4/8/12/16/20/24/32 px spacing scale. Specify component
padding and the gaps between its children separately. Parent padding does not
replace spacing inside a status row, confirmation summary or permission item.
Detail views wrap long names, codes and descriptions inside their allocated
column. Dense product lists clamp names to two lines, reserve the same two-line
height for short names, and truncate article codes to one line. Expose the full
name through `title` and the product drawer. A long name must not stretch one
table row into a tall card; measure representative row heights after clamping.

## Controls, drawers and scrolling

- Style closed, open, selected, focused, disabled and hovered dropdown states.
  Products use native select semantics with `appearance: base-select` picker
  styling inside a feature query. Keep the usable native fallback, labels,
  keyboard interaction and form change events intact.
- Center an overlaid provider icon against the control's height, not a text
  baseline. Reserve its width in the control's padding and keep it outside the
  pointer target.
- Size a filter for its longest predefined value **plus** horizontal padding,
  the text/icon gap and the picker arrow. Prevent flex shrink from stealing that
  space; let the surrounding toolbar wrap first. Check every selected value at
  the compact desktop width. `overflow: hidden` is not a sizing fix. In the
  expanded mobile filters, source and lifecycle each occupy a full grid row.
- Use a padded segmented-control container with individually rounded preset
  buttons and an unambiguous selected state.
- A matching drawer has a header, an independently readable source summary, a
  search/results area and explicit actions. Show identifiers through disclosure
  when they would crowd the primary product identity.
- A bounded result list owns its vertical overflow and cannot shrink below its
  intended content region. Pagination follows the list in normal flow.
  Expanded supplementary details also stay in normal flow.
- Put table pagination outside the table's scrolling region, with its own padding
  and separator. Sticky headers/footers need a defined scroll owner, opaque
  background and enough reserved space to keep content reachable.
- The product detail drawer uses a scrollable `.products-detail-body` and a
  separate non-scrolling action footer. Its outer content wrapper has no padding,
  does not scroll and reserves no scrollbar gutter; padding belongs to the body and footer.
  Do not make the action footer sticky inside the same padded scrolling body.
- Below 600 px, policy rows and automation permissions become stacked cards;
  below 700 px, history rows become cards. Remove the table's desktop minimum
  width at the same breakpoint. Keep permission field names and select-all
  available after hiding column headers. Dense tables with different columns
  need separate mobile selectors, even if they share the desktop table frame.
- Empty search results need a useful next action, such as clearing the search;
  a disabled pager and an otherwise blank drawer are insufficient.

## Visual review before reporting completion

Use the existing local preview in the user's visible browser when requested.
Inspect actual component screenshots, not only the whole page scaled to fit.

1. Check every affected page and nested form/drawer at desktop and narrow mobile
   widths. Include a short viewport when a footer or pager is involved.
2. Open dropdowns and accordions. Exercise selection, keyboard focus, disabled
   actions, loading, empty results, validation and long content where available.
3. Inspect computed padding, margin, gap, font and dimensions for representative
   elements. For overlap reports, compare the actual bounding rectangles of the
   scroll region, pager and following disclosure.
4. Review the first and last visible list rows and scroll to the end. Check that
   menus escape clipping where appropriate and that the page has no unintended
   horizontal overflow.
5. Save the relevant screenshots and record exactly which states were observed.
   Separate fixture rendering from checks against an actual local API. Do not
   use screenshots to claim provider writes, database contracts or production
   acceptance that were not exercised.
6. Choose executable checks with `make check-plan`. Builds and type/lint checks
   complement visual inspection. Follow the user's current test restrictions;
   do not launch broad regression to validate a spacing change.
