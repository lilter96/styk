# Integrations UX V1

The Integrations page now presents two selected provider accounts, their access and catalog evidence, and the next useful action. It uses the existing import APIs and worker; there are no backend, credential-storage, authentication, connector or publication changes. The working tree was clean at the start, at `7c38c71` (newer than the commit named in the brief). `web/src/style.css` was left untouched.

## Before and after

The supplied `web/test-results/import-panel-moysklad.png` was inspected before implementation alongside Integrations, ImportPanel, ImportSubmission and both stylesheets. The original screen had two full operation consoles beneath verbose provider cards. Its repeated scope identifiers, raw status/outcome enums, diagnostic metrics and seven-column history table obscured import actions. Labels and counters used 10–12 px text, low contrast and horizontal table scrolling. Configuration, registration, connection checks and imports competed without a clear hierarchy. The main workflow required unnecessary scrolling; historical details opened automatically.

The new screen uses three primary areas: the Russian page heading, two provider cards and contextual next steps. History follows as compact provider rows showing the latest result, date and useful summary. Each provider's bounded history and individual entries expand deliberately. Provider cards show account registration separately from actual connection-check evidence and the last eligible selected-account import. A stored import never implies live connectivity or inventory publication readiness.

Design retains STYK's dark navigation and green action color. The palette uses ink `#203d42`, muted text `#5d7377`, canvas `#f3f6f7`, white `#ffffff`, borders `#dce6e5` and action green `#187661`. Existing sans-serif typography uses a 32 px page heading, 22 px provider headings, 15–16 px body/actions and 14 px secondary text. Whitespace and alignment separate connection evidence, catalog counts and the action; color supplements written status. Native disclosure controls, visible keyboard focus, confirmation focus restoration/Escape handling and reduced-motion support remain available. No component library, font dependency or new framework was added.

## Workflow

1. Open a workspace from the unobtrusive header selector. Choose a provider account in **Настройки**. Multiple accounts use numbered entries and registration dates; the matching numbered identifier list can be inspected/copied under **Технические детали**.
2. If needed, explicitly **Проверить подключение**, acknowledge account ownership and **Добавить аккаунт**. A successful check within five minutes is still required for registration; unknown registration outcomes require reconciliation before another attempt.
3. Choose **Загрузить данные** or **Обновить данные**, inspect the provider/account scope and explicitly acknowledge and confirm the import. Opening the page, changing scope, checking access or following next-step guidance never submits an import.
4. Read the actual phase and observed product count while the worker runs. Counters are explicitly attempt-local, not committed quantities. No percentages or invented totals are displayed. Unknown or failed operations retain their visible warnings and recovery actions.
5. Once both selected accounts have eligible completed imports, follow **Сопоставить товары** or **Сопоставить склады**. These navigate to existing review screens; they create no mappings or inventory publication. While an initial import is active/unknown, the next step asks the user to wait rather than request another import.

## Progressive disclosure and safety

**Настройки** contains connection testing, explicit registration, account selection and contextual import help. **Технические детали** contains tenant/account/operation/batch identifiers with copy controls, raw phase/status/outcome, request/HTTP/page counters, attempt counts and resource limits. No raw marketplace responses are rendered. Business errors, uncertain submission outcomes and queued cancellation remain accessible where they matter.

Client-generated UUID idempotency keys and exact serialized request bodies are unchanged. Uncertain retries retain the original tenant/provider/account/type and replay the identical body. Switching accounts cannot retarget a pending intent; restoration returns to its original scope. Session loss and conflicts keep the history-inspection gate. Request keys and credentials are not stored in browser storage. Reload retains only selected scope and recovers durable server history; a pending key remains memory-only, as before. No import submits automatically.

Each selected provider keeps a scoped latest-history query even with history collapsed or an older page displayed: ten rows per request, bounded offset, explicit pagination. Live operation polling remains independent of historical selection. Completion invalidates the existing related queries even while older history is inspected. Cancellation requires explicit confirmation and is offered only for queued operations; the latest known state is checked again and the server still arbitrates races. Previous successful evidence remains available after failures, limits and unknown outcomes. Duration uses started-to-finished timestamps where available; attempt-only elapsed time is labelled accordingly.

## Responsive and visual verification

The real React application was rendered with Playwright and bounded synthetic API fixtures. Desktop 1440 × 900 and laptop 1280 × 800 show both provider states/actions and contextual next steps in the initial successful-state viewport. At 390 × 844 the cards stack, navigation remains labelled, primary controls are at least 44 px high, and no horizontal overflow occurs. Detailed history and identifiers stay collapsed. Active/error warnings naturally increase page height.

All nine final screenshots below were visually inspected and preserved outside Playwright's disposable output directory:

| Scenario | Screenshot |
| --- | --- |
| Both imported, desktop | [success-1440.png](screenshots/integrations-v1/success-1440.png) |
| Both imported, laptop | [success-1280.png](screenshots/integrations-v1/success-1280.png) |
| Both imported, mobile | [success-390.png](screenshots/integrations-v1/success-390.png) |
| Ozon not registered | [missing-1440.png](screenshots/integrations-v1/missing-1440.png) |
| Running import | [running-1440.png](screenshots/integrations-v1/running-1440.png) |
| Failed import | [failed-1440.png](screenshots/integrations-v1/failed-1440.png) |
| Worker result unknown | [unknown-operation-1440.png](screenshots/integrations-v1/unknown-operation-1440.png) |
| Submission response lost | [unknown-submission-1440.png](screenshots/integrations-v1/unknown-submission-1440.png) |
| Expanded technical details | [technical-details-1440.png](screenshots/integrations-v1/technical-details-1440.png) |

Rendered HTML, browser storage and API-response assertions check synthetic provider credentials are absent. Default-visible content checks tenant/account/operation identifiers and internal enums are absent. Technical screenshots contain synthetic identifiers only. No live marketplace API was contacted.

## Verification results

- `npm run lint`: pass.
- `npm run typecheck`: pass.
- `npm test`: 79/79 across eight files, including account scope, immutable retries, unknown outcomes, polling, pagination, cancellation races, explicit registration, progressive disclosure and next-step guidance.
- `npm run build`: pass.
- `npx playwright test e2e/integrations-visual.spec.ts --workers=1`: 9/9 pass.
- Existing import browser harness: Ozon and MoySklad branches pass against the real Go API/shared worker and dedicated migrated PostgreSQL with synthetic sources. Includes both submissions, identical lost-response replay, provider/account switching, polling, queued cancellation, durable history after reload, SQL integrity and Products/Warehouses/Diagnostics navigation. No publication tasks are created.
- Existing connection browser harness: pass, including explicit registration, access check, lost registration response reconciliation and ownership conflict.
- Existing warehouse review, product/warehouse confirmation, smoke and product review browser assertions: all pass (five scenarios). Smoke/review ran from temporary copies with isolated local ports and the synthetic login secret adapted because unrelated local processes occupied their hardcoded ports; those copies and test processes were removed.
- `make lint`, `make test`, `go test -race ./...`, `make build`: pass.
- `git diff --check`: pass. No backend contracts, Go files or migrations changed.

Reproduce visual checks from `web` using the command above (Vite is started on loopback port 15176). Reproduce real import checks with a dedicated local database migrated through version 22:

```sh
STYK_IMPORT_BROWSER_E2E=1 TEST_DATABASE_URL='<dedicated local test database URL>' \
  go test -tags=integration -count=1 -run '^TestImportManagementBrowser$' -v ./internal/console
```

The connection regression uses `STYK_BROWSER_E2E=1`, a separate dedicated test database and `-run '^TestConnectionBrowser$'`. The existing safety checks and no-live-services restriction still apply.

## Remaining limitations

No blocking UX defects remain in this milestone. The API has no friendly account/workspace names, so account identification uses numbered registration entries with exact identifiers available on demand. Products, Warehouses, Diagnostics, Overview and sign-in retain their existing detailed/English interfaces; this milestone deliberately redesigns Integrations only. Mobile requires vertical scrolling through the two provider cards. Resource diagnostics become long when explicitly expanded. These are scope boundaries, not claims of synchronization readiness.
