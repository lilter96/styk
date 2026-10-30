# Структура репозиториев

## Границы

Основной объект E:/work/styk — один Go module, одна React console, SQL migrations, тесты, Dockerfile, Compose и GitHub Actions. .sln/.csproj не обнаружены; .NET/EF Core отсутствуют. E:/work/styk-demo и E:/work/styk-landing — соседние проекты, не каталоги Go repository. Наличие .git подтверждено у landing; отдельная история версий standalone demo в этом аудите не проверялась.

Незакоммиченные изменения существовали до аудита: auth removal, frontend redesign, tests и docs. Отчёт описывает working tree, не один release commit. Не выдавать git HEAD за точный снимок текущей реализации.

## Карта

```text
styk/
  cmd/
    api/                     HTTP API и health
    worker/                  imports, mock tasks, publication/catalog work
    claim-workspace/         доверенная миграция legacy ownership
    demo/                    mock inventory task, не React demo
    doctor/                  инженерная диагностика
    matching/                trusted product mapping CLI
    warehouse-mapping/       trusted warehouse mapping CLI
    moysklad-import/         read-only source import
    ozon-import/             read-only marketplace import
    ozon-postings/           read/inspect posting evidence
    moysklad-orders/         read customerorder evidence
    stock-commitment/        persisted calculation/registration
    moysklad-fixtures/       контролируемые test fixture tools
  internal/
    domain/                  source observations, scopes, mapping/stock/order types
    application/             workflows, readers, worker orchestration
    catalog/                 independent catalog facts, policies, plan, validation
    catalogsync/             reviewed durable catalog workflow
    catalogprovider/         scoped provider/credential factory
    commitment/              stock proof/calculation rules
    matching/                deterministic product candidates
    ordercorrelation/        identity/evidence matching of orders
    identity/                users, sessions, verification, reset, SMTP
    credentials/             encryption and scoped resolver
    postgres/                SQL, transaction boundaries, actor guards, persistence
    infrastructure/
      moysklad/              provider read + controlled catalog adapters
      ozon/                  provider reads + controlled writes
    stocksync/               publisher and automatic cycle runners
    testpublication/         fixed owner test scope and gates
    console/                 business HTTP handlers and merchant auth
    transport/               liveness/readiness
    config/                  process config and publication gates
    doctor/                  diagnostics
    doctordemo/              diagnostic synthetic support
    moyskladfixture/         experimental fixture orchestration
    ozonjob/                 durable catalog import runner for both providers
  migrations/                44 ordered Goose SQL migrations
  tests/integration/         DB/concurrency/security/browser-backed test programs
  web/
    src/                     real console
    e2e/                     Playwright and fixture seed
    src/assets/fonts/        bundled Inter and JetBrains WOFF2 assets
    package.json             frontend dependencies and scripts
    vite.config.ts           local proxy
  .github/workflows/         CI
  docs/                      architecture/history/lab evidence and current audit
  AGENTS.md
  go.mod, go.sum
  Makefile
  compose.yaml
  Dockerfile
styk-demo/
  src/main.tsx               synthetic application and routes
  src/Hero3D.tsx             procedural WebGL scene
  src/style.css
  public/brands/
styk-landing/
  src/main.tsx               marketing and demo previews
  src/ConnectionScene.tsx    animated SVG integration scene
  src/style.css, connection-flow.css
  public/brands/, legal/
  demo/                      separately built copy of demo
  scripts/prerender.mjs
  scripts/copy-demo.mjs
```

Служебные .local, bin, dist, node_modules, .git internals и временные файлы из дерева намеренно исключены. Их содержимое не является частью передаваемого архива.

## Зависимости компонентов

API → config, postgres, console, identity.SMTP, credentials, transport. Worker → config, postgres, application.Worker, ozonjob, stocksync, catalogsync/catalogprovider. Trusted CLI → application/domain/postgres и соответствующий adapter. Console → net/http и application/domain/postgres; SQL в отдельных HTTP-обработчиках означает неполную чистоту слоёв.

Shared libraries отдельного solution нет; переиспользование через internal packages. Dependency injection ручная, через struct fields и небольшие Go interfaces. Нет MediatR, EF, generic repository или отдельного message broker.

Frontend зависит от HTTP /api; backend не отдаёт собранный SPA. Landing build сначала собирает nested demo, затем landing, prerender и copy-demo; это важная отдельная release-зависимость. Standalone и nested demo src/main.tsx на момент чтения байтово совпадают, но остаются двумя копиями.

Полный автоматически извлечённый список существенных каталогов и entrypoint declarations добавлен ниже.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [go.mod:1](../../go.mod#L1) | configuration / declarations; see file |
| [Makefile:1](../../Makefile#L1) | configuration / declarations; see file |
| [Dockerfile:1](../../Dockerfile#L1) | configuration / declarations; see file |
| [compose.yaml:1](../../compose.yaml#L1) | configuration / declarations; see file |
| [cmd/api/main.go:1](../../cmd/api/main.go#L1) | 21: main; 27: run; 66: configuredHandler; 90: apiForMode |
| [cmd/worker/main.go:1](../../cmd/worker/main.go#L1) | 28: catalogClient; 85: moyskladCatalogClient; 142: main; 148: run |
| [E:/work/styk-landing/package.json:1](E:/work/styk-landing/package.json#L1) | configuration / declarations; see file |
| [.github/workflows/ci.yml:1](../../.github/workflows/ci.yml#L1) | configuration / declarations; see file |

## Entrypoint files

| Command | Source files |
|---|---|
| `api` | `cmd/api/main.go` |
| `claim-workspace` | `cmd/claim-workspace/main.go` |
| `demo` | `cmd/demo/main.go` |
| `doctor` | `cmd/doctor/main.go` |
| `matching` | `cmd/matching/main.go` |
| `moysklad-fixtures` | `cmd/moysklad-fixtures/main.go` |
| `moysklad-import` | `cmd/moysklad-import/main.go` |
| `moysklad-orders` | `cmd/moysklad-orders/main.go` |
| `ozon-import` | `cmd/ozon-import/main.go` |
| `ozon-postings` | `cmd/ozon-postings/main.go` |
| `stock-commitment` | `cmd/stock-commitment/main.go` |
| `warehouse-mapping` | `cmd/warehouse-mapping/main.go` |
| `worker` | `cmd/worker/catalog.go`, `cmd/worker/main.go` |

## Frontend source inventory

- `web/src/api.ts`
- `web/src/App.tsx`
- `web/src/AppShell.tsx`
- `web/src/AutomaticStockControls.tsx`
- `web/src/catalog-sync.css`
- `web/src/CatalogSync.tsx`
- `web/src/CatalogSyncAttribute.tsx`
- `web/src/CatalogSyncAutomation.tsx`
- `web/src/CatalogSyncConfig.tsx`
- `web/src/CatalogSyncCreation.tsx`
- `web/src/CatalogSyncFacts.tsx`
- `web/src/CatalogSyncOperations.tsx`
- `web/src/CatalogSyncPreview.tsx`
- `web/src/CatalogSyncProducts.tsx`
- `web/src/CatalogSyncTypes.tsx`
- `web/src/CatalogSyncValue.tsx`
- `web/src/Confirmation.tsx`
- `web/src/CredentialEditor.tsx`
- `web/src/CredentialSubmission.tsx`
- `web/src/design-system.css`
- `web/src/ImportPanel.tsx`
- `web/src/imports.css`
- `web/src/ImportSubmission.tsx`
- `web/src/IntegrationDialog.tsx`
- `web/src/IntegrationEvents.tsx`
- `web/src/Integrations.tsx`
- `web/src/main.tsx`
- `web/src/mapping.css`
- `web/src/MerchantAuth.tsx`
- `web/src/OrderEvidence.tsx`
- `web/src/ProductMappingPage.tsx`
- `web/src/ProductReview.tsx`
- `web/src/StockShadow.tsx`
- `web/src/StockSyncControls.tsx`
- `web/src/style.css`
- `web/src/SupportDiagnostics.tsx`
- `web/src/UI.tsx`
- `web/src/WarehouseReview.tsx`
- `web/src/WorkspaceSwitcher.tsx`

## Snapshot provenance

Git HEAD (does not include pre-existing working-tree edits): `2a294ada95d066c5bda26a9adc75f589d98b817b`. Audited working-tree paths may differ from this commit.
