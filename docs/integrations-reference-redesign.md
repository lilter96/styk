# Integrations: reference redesign and scoped publication V1

> **Safety audit hold:** actual marketplace stock writes are disabled in code. UI indicates unavailability; see [stock-publication-safety-audit.md](stock-publication-safety-audit.md). Historical screenshots and test descriptions below represent the pre-audit redesign.

The implementation follows the seven screens in the supplied local `Интеграции — эталонный редизайн.html`: navigation, setup steps, provider cards, key editor, publication confirmation, progress, support diagnostics and product mapping. The user subsequently explicitly extended scope to encrypted key storage and stock publication. Those are implemented backend workflows, not simulated controls.

## UX assessment

Before: expanding one provider's settings stretched the entire two-column row, leaving a large empty area in its neighbour. Import history and technical identifiers competed with connection status and the primary action. Tiny labels, duplicate status messages, English internal terms and several permanently expanded diagnostic surfaces made the next action difficult to find.

After: the initial desktop view shows the setup checklist, both provider states and their actions. Settings and confirmations open in bounded, keyboard-accessible dialogs; expanding technical information cannot change provider-card geometry. A restrained blue/white/grey palette, Inter typography, 28px page titles, 20px provider headings, 15–16px body text and consistent spacing follow the reference. `web/src/style.css` was preserved.

## Information architecture and workflow

1. Choose a workspace/account using the header popover. Friendly connection names appear in account selectors; technical identifiers are disclosed separately.
2. Connect a provider or update its key. Saving requires an explicit ownership acknowledgement and a real bounded connection probe. Keys are encrypted on the server. The existing server-configured probe and explicit account registration remain available in advanced settings.
3. Load or refresh each catalog from its provider card. A separate confirmation is required; no import is automatically submitted. Running imports show real phases and observed attempt-local counts, without invented totals or percentages.
4. Review product and warehouse mappings explicitly. Product progress counts only the selected completed Ozon catalog and exact selected account pair; historical mappings do not inflate progress.
5. If mappings and current evidence permit it, preview stock-publication conditions, review quantities and explicitly enable publication. A completed import or a mapping alone never establishes publication readiness.

The main event feed contains at most three recent scoped events. Full provider histories remain paginated in a separate dialog (10 operations per request). Operation/batch/account/workspace IDs, raw phases, safe outcome codes, HTTP/page counters and limits remain under technical disclosures. A support dialog copies only allowlisted metadata; raw provider responses and credentials are excluded.

## Responsive and accessibility behaviour

Verified at 1440×900, 1280×800 and 390×844. Provider cards stack on mobile; primary actions remain readable and easy to tap. Mapping tables become labelled row cards. Dialogs fit the viewport and scroll internally, retain visible close controls, trap keyboard focus, close with Escape and restore focus. Disclosure/settings expansion preserves the geometry of the main page and introduces no horizontal document overflow. Indeterminate import animation respects reduced-motion preferences.

## Existing workflow guarantees

Both existing import workflows retain explicit confirmation, client-generated immutable idempotency keys, identical retries after lost responses, original tenant/provider/account scope, account-switch protection, durable history, polling, refresh recovery and queued-only cancellation. A previously accepted import can still be recovered after credential disconnection; a fresh import is rejected. Failed and uncertain outcomes remain visible. Imports do not publish inventory or create mappings.

## Added backend capabilities and limits

See [credential storage](credential-storage-v1.md) and [stock publication](stock-sync-v1.md) for exact contracts, migration implications and operational requirements.

- API and worker require the same separately supplied `STYK_CREDENTIAL_ENCRYPTION_KEY` for encrypted stored keys. An absent key leaves the existing environment-based import workflow available and disables saving. Malformed supplied configuration fails startup. No master key is generated or committed.
- Stock publication is conditional and bounded to 100 product/warehouse targets. Imported catalogs do not establish all cross-system reservation, order, baseline or competing-writer guarantees. Missing critical facts remain blocked. Scoped, expiring operator attestations can supply independently checked evidence through an advanced interaction; the software does not describe that evidence as independently verified by the platform.
- The publisher performs one external write attempt, preserves per-target outcomes and fences uncertain writes globally by Ozon account/product/warehouse. It never retries an uncertain marketplace write automatically. A matching readback proves only the observed stock, stops automatic publication and requires a new operation-bound settlement assertion and fresh source evidence before explicit resumption.
- Enabled publication reevaluates newly imported eligible evidence while its original conditions remain valid. It does not schedule imports. Expired evidence/proofs, changed mappings/credentials and unknown outcomes block further writes.
- The existing trusted local-operator session remains in use. This milestone does not add production tenant authentication.

## Screenshots

All screenshots were rendered with Playwright using the actual React application and synthetic data, then visually inspected. No live marketplace endpoint was contacted.

- [Desktop 1440](screenshots/integrations-reference/success-1440.png), [laptop 1280](screenshots/integrations-reference/success-1280.png), [mobile 390](screenshots/integrations-reference/success-390.png).
- [Missing provider](screenshots/integrations-reference/missing-1440.png), [invalid key](screenshots/integrations-reference/key-error-1440.png), [running import](screenshots/integrations-reference/running-1440.png), [failed import](screenshots/integrations-reference/failed-1440.png).
- [Unknown submission](screenshots/integrations-reference/unknown-submission-1440.png), [unknown execution](screenshots/integrations-reference/unknown-operation-1440.png), [unavailable history](screenshots/integrations-reference/history-unavailable-1440.png).
- [Key editor](screenshots/integrations-reference/update-key-1440.png), [settings](screenshots/integrations-reference/settings-1280.png), [mobile technical details](screenshots/integrations-reference/technical-390.png), [operation details](screenshots/integrations-reference/operation-details-1440.png), [support](screenshots/integrations-reference/support-1440.png).
- [Publication confirmation](screenshots/integrations-reference/enable-sync-1440.png), [enabled publication](screenshots/integrations-reference/sync-active-1440.png).
- [Product mapping](screenshots/integrations-reference/mapping-1280.png), [mobile mapping](screenshots/integrations-reference/mapping-390.png), [mapping review](screenshots/integrations-reference/mapping-review-1280.png), [warehouse review](screenshots/integrations-reference/warehouse-review-1280.png).

## Verification

Frontend lint, TypeScript, 111 unit tests and production build; existing real API/worker import browser tests for both providers; connection registration/probe browser test; mapping/warehouse confirmation and read-only navigation browser regressions (8 real API browser cases in total); 20 synthetic Playwright visual/interaction cases at the specified viewports; Go lint/vet, unit tests, race detector, build and full PostgreSQL integration suite. New credential/publication regressions run separately under the race detector against dedicated local databases. Migrations are verified forward and backwards on disposable databases, with refusal to erase nonempty credential/publication evidence. All checks passed. Publication-specific PostgreSQL race coverage contains 9 scenarios, including authenticated real HTTP preview/enable/replay/disable.

## Remaining UX tradeoffs

The advanced manual publication-evidence form is intentionally more involved than the ordinary seller workflow because the existing imports cannot prove all critical order/reservation and competing-writer conditions. Missing facts are explained and keep publication blocked. Long histories and diagnostics require scrolling inside their explicitly opened dialogs. Ozon import data supplies offer IDs without product names; the mapping view displays those actual values. Additional production authentication, automatic import scheduling and marketplace connectors remain outside this milestone.
