# STYK — полный контекст для следующего ChatGPT / Principal Architect

## 1. Как использовать этот документ

Это переносимый технический handoff по состоянию на 30 октября 2026. Для понимания базовой картины исходный код не требуется. Остальные 17 файлов уточняют матрицу функций, архитектуру, API, таблицы, security и roadmap. Ссылки на source указывают реальные paths/символы и line evidence; исходники в ZIP намеренно не включены.

Аудит только читал source/config и создал docs/audit. Не выполнялись builds/tests/migrations/seed/provider requests. Значения секретов/.env/пользовательские записи не читались. Предыдущие результаты исполнения — отдельно помеченная история беседы, не новый verification.

Классификация фактов: CONFIRMED — непосредственно код; INFERRED — обоснованный вывод; UNKNOWN — недостаточно данных; PLANNED — бизнес-намерение. IMPLEMENTED означает кодовую цепочку, не перепроверенный production success. Не превращай факты UI/DTO/README в доказанную integration capability.

## 2. Что такое STYK и какую проблему решает

PLANNED: SaaS для продавцов маркетплейсов России и Беларуси. Учётная система МойСклад должна связываться через STYK с несколькими кабинетами Wildberries, Ozon и Яндекс Маркета. Пользователь хочет меньше ручного переноса товаров/остатков/заказов, понятные сопоставления и единый рабочий кабинет.

Пример целевого клиента: 3 WB кабинета, 5 Ozon, 1 Yandex, 1 MS. Ориентир цены 5 000 RUB/месяц — гипотеза владельца, не реализованный billing. Предполагаемое ООО РБ с участниками 50/50 не подтверждено юридическими реквизитами в коде.

CONFIRMED: реальная система сейчас значительно уже — инженерный MS/Ozon контур с read imports, mappings, доказательствами остатков и ограниченными test write workflows. Не называй её готовой автоматизацией трёх маркетплейсов.

## 3. Реальный stack

Backend — Go, а не .NET. go.mod/Makefile/Dockerfile декларируют Go 1.27.3. PostgreSQL с pgx/v5, SQL Goose migrations, net/http.ServeMux, slog, contexts. Нет ASP.NET Core, EF DbContext, MediatR, .NET DI или solution projects. Dependency injection ручная через constructors/struct fields/interfaces.

Frontend — React 19, TypeScript 5.9, Vite 6, React Router 7, TanStack Query 5. Plain CSS и локальные UI primitives. В standalone demo дополнительно Recharts, motion, lucide-react, Three.js/react-three-fiber/drei. Landing — отдельный React/Vite проект со встроенной копией demo.

PostgreSQL image в Compose/CI — 17.6. Runtime Docker image distroless nonroot. Redis/broker/object storage как обязательные зависимости не найдены.

## 4. Архитектура и основные компоненты

Модульный монолит, два независимых процесса:
- cmd/api: HTTP server, merchant identity, scoped business handlers, health.
- cmd/worker: durable imports, mock tasks, stock publication/recovery/automatic и opt-in catalog work.
Они разделяют PostgreSQL и outbox, а не общую память процесса.

internal/domain содержит типы наблюдений/scopes/mapping/stock/orders. internal/application оркестрирует read workflows и worker. internal/postgres владеет SQL/transactions. internal/infrastructure/{moysklad,ozon} — provider adapters. internal/identity — users/password/email/sessions/SMTP. internal/credentials — encryption/resolver. internal/matching — product candidates. internal/commitment — conservative stock evidence/calculation. internal/catalog — independent field model/policies/plans. internal/catalogsync — config/preview/approval/outbox/receipts/readback. internal/stocksync — stock publishers и automatic cycles. internal/console — business HTTP/auth; internal/transport — только health.

Generic repositories/CQRS/framework services не внедрены. Некоторые console handlers содержат SQL и coordination, postgres.Store широкий; это долги поддерживаемости, не причина переписывать весь проект.

Есть trusted administrative CLI commands: imports, matching, warehouse mapping, order/posting evidence, stock commitment, doctor, legacy workspace claim. cmd/demo — submit mock outbox task; это не React demo и не реальная синхронизация.

## 5. База данных

44 SQL миграции (00001–00044). Главные группы:
- saas_users/workspaces/memberships/sessions/reset/verification/security_events/auth_limits/merchant_accounts;
- stock_connections и connection_credentials/credential_save_requests;
- source_imports/entities/aggregate/per-warehouse observations;
- ozon catalog batches/products/warehouses/FBS stock observations;
- product/warehouse mappings, histories, reviews, confirmation audits/idempotency;
- posting/customerorder batches/identities/positions;
- stock pools/policies/decisions/evidence/publications/targets/readbacks/recovery/fences;
- automatic configs/cycles/initial publications;
- catalog configs/revisions/enrollments/snapshots/previews/approvals/operations/work/commands/attempts/receipts/baselines/rebases/dispatch_intents/readbacks;
- outbox_tasks и mock inventory_observations.

Импортные наблюдения — история обхода, не provider snapshot. Nullable/missing не равны zero. Composite keys/FKs/uniqueness закрепляют scoped lineage. История/аудит местами immutable triggers. Down migrations могут преднамеренно отказать при наличии business data; не запускать down как универсальный rollback.

Полный migration/table/FK/index inventory — в 07. Фактическая установленная schema version текущей БД в этом аудите не проверялась.

## 6. Авторизация и tenant isolation

Работает кодовый email flow: pending registration → email verification link → пользователь задаёт password → Authenticate → persistent opaque cookie session. bcrypt cost 12, password bounds 12–72 bytes, ограничение hash concurrency. В БД хранится digest bearer token, отдельно CSRF и audit ID. Session максимум 8 часов, checks revoked/disabled/email_verified/auth_version. JWT/refresh/OAuth/MFA нет; это не означает, что нужно заменить рабочие cookies на JWT.

Каждый protected handler проходит merchantAuth.authorized: Host/Origin/direct loopback peer → session → CSRF → MerchantTenant → scope/object authorization → handler. Server injects own tenant, mismatch отклоняется. Review object привязан к session audit. В SQL mutation повторяется actor permission под locks; migration 42 закрывает expiry-after-lock-wait проблему повторной проверкой.

Существенный нюанс: migration 39 saas_merchant_accounts обеспечивает user↔tenant one-to-one; MerchantTenant требует OWNER. Хотя membership enum содержит ADMIN/OPERATOR/VIEWER, полноценная multi-user organization/RBAC UI не реализована.

RLS не найдена. Scoped SQL/constraints/HTTP — граница приложения, прямой DB access остаётся административным. requireContextActor намеренно допускает no-identity-context для trusted CLI/worker. Новая HTTP ручка не должна случайно попасть на этот trusted путь.

## 7. Удалённый вход по общему секретному ключу

Предыдущий этап убрал UI key form, runtime key comparison/creation, environment setting и normal local auth bootstrap. STYK_AUTH_MODE=local отвергается; /api/session остаётся действующим email/password endpoint. secret payload rejects unknown field. Нельзя удалить весь /api/session или открыть кабинет анонимно.

Однако current auth.go всё ещё содержит operatorSession/map/fallback cookie authorization/logout. Synthetic session creation есть в test-only fixture и integration-tag file, с executable suffix guard. Это legacy/test debt, а не доступный через normal bootstrap ключевой login. Не заявляй «удалена каждая строка старого auth»; следующий cleanup должен убрать остатки безопасно и не ломать business tests. Integration build tag/имя binary не являются security boundary для атакующего с deploy доступом.

## 8. Credentials и внешние accounts

Поддержаны MS bearer token и Ozon Client-ID/API-Key. Они server-side, encrypted AES-GCM в PostgreSQL. AAD включает tenant/provider/account/revision; HMAC request digest. Ordinary metadata responses не содержат plaintext. Master key берётся из environment; versioned rotation/KMS workflow не найден.

Save/probe проверяет provider identity/ownership, revisions и idempotency. Disconnect disables credential и меняет lifecycle; source history не удаляет. Frozen operations проверяют credential revision. Environment fallback возможен для зарегистрированных accounts.

Несколько accounts одного provider представлены composite records; registry UNIQUE(source,account_id) запрещает назначать один external account разным tenant. Это нужно согласовать для агентств/команд.

## 9. Какие интеграции реально существуют

MS: products/variants/stores, aggregate и per-store stock reports, customerorder/details/positions, catalog metadata/references и controlled catalog write.
Ozon: catalog/products/names, warehouses, FBS stocks, postings list/details, category/attribute/dictionary reads, task polling, controlled create/text update и fixed-test stock writes.
WB/Yandex backend adapters отсутствуют; бренды и mock accounts есть в demo/landing.

Read clients имеют URL/method allowlists, bounded response/page/entity budgets, per-instance rate limits, bounded retries/Retry-After. Distributed account quota coordination отсутствует. Provider contracts/permissions/quotas на сегодняшний день внешне не проверялись.

## 10. Как выполняются импорты и mappings

Console создаёт scoped durable import operation с idempotency → DB outbox → worker claim/lease/token → provider reads вне transaction → atomic commit observations + fenced job result → UI polls progress.

Read retries допустимы с budget, но lease recovery mock/import нельзя переносить на arbitrary external writes. Import completed не доказывает snapshot completeness или current connectivity.

Product matching сравнивает seller offer_id с MS article/code, не считает name совпадение identity. Кандидат → explicit reviewed choice → frozen import revisions/session review → confirm under locks → mapping/history/audit/idempotency. Warehouse mapping аналогично, с eligibility/one-to-one ограничениями. Human confirmation не доказывает physical unit/warehouse/stock safety.

## 11. Остатки и безопасность публикации

Модель различает stock/reserve/inTransit/available и Ozon present/reserved/free_stock. Ничего не выводится из отсутствующей строки. Commitment объединяет observed stock, mapping versions, policy age, units, obligations/reserve/coverage proofs, unseen reduction/safety bounds. Результат может быть UNKNOWN/BLOCKED вместо удобного числа.

Stock publication intent фиксируется до dispatch; final guards проверяют scope/evidence/settings. Ответ provider читается per target; uncertain/partial outcomes сохраняются, выполняется readback/reconcile. Нельзя blind retry после timeout или считать 200 общим success.

Главная граница: обычная real-host production publication для произвольных клиентов закрыта. Есть loopback simulator и отдельно fixed owner-authorized test scope. Реальные test accounts не означают buyer-isolated sandbox. Не расширять authorization из прежнего экспериментального разрешения на остальных клиентов/targets/quantities.

## 12. Catalog synchronization

Это реальный restricted workflow, а не только DTO. Field authorities: MS authoritative / Ozon authoritative / manual / ignore. Baseline fingerprints помогают обнаруживать conflicting edits и избегать ping-pong. Preview freezes revisions и selected values, validation/capabilities; approval выбирает whole reviewed products.

Worker claims command, проверяет hash/config/mapping/credential scope, перечитывает source/target, binds exact writer intent, журналирует dispatch, отправляет с timeout. States: QUEUED/CLAIMED/DISPATCHING/PROCESSING/ACKNOWLEDGED/UNKNOWN/RECONCILED/REJECTED/BLOCKED. Accepted task/ACKNOWLEDGED не равны verified readback. UNKNOWN сохраняет target fence.

Ozon existing-product update допускает known name/description, не generic arbitrary fields. Create — отдельная schema-validated branch/task receipt. MS catalog payload шире (часть text/price/barcodes/reference/attributes/create/archive), но не документы/stock movements/image uploads. Generic FieldPrice/Images не означает готовый массовый repricing или image pipeline.

Worker catalogWork вообще включается opt-in fixed test scope. Public production scope не становится writable от включения UI.

## 13. Заказы, цены и аналитика: важное различие

Ozon postings и MS customerorders читаются/сохраняются как evidence для stock/correlation. Есть scoped identities и progress. Нет общего production order management workflow: создание заказов в MS, fulfillment/labels/shipping/cancel/returns/status writes отсутствуют.

Price может присутствовать в facts и ограниченном catalog payload. Нет общего /pricing модуля с наценками, комиссиями, массовыми правилами/историей.
Выручка/прибыль/расходы/комиссии/межаккаунтная analytics отсутствуют в backend; красивые metrics/charts в demo синтетические.

## 14. Как устроен реальный frontend

Маршруты: / overview, /products mapping, /warehouses observations/review, /integrations credentials/imports/stock controls, /catalog-sync restricted review workflow, /diagnostics Doctor. MerchantAuth заменяет app при отсутствии session; verify/reset tokens в URL fragment.

TanStack Query управляет server cache, React — локальным состоянием; api.ts добавляет CSRF и сообщает expiration. Sensitive bearer cookie HttpOnly. При смене session cache/selected state сбрасывается. Mutation hooks сохраняют pending/unknown intents, чтобы uncertainty не превратилось в повторную внешнюю команду.

AppShell/UI создают sidebar/mobile drawer/states/dialogs. Plain CSS layers с design-system overrides, purple tokens, локальные шрифты. Нет production financial chart library/3D scene в рабочем app. Responsive/accessibility код есть, независимого browser QA в этом аудите нет.

## 15. Лендинг и два демо

E:/work/styk-demo — standalone Vite application. E:/work/styk-landing — marketing app с nested demo/ и build scripts. Оба demo main.tsx байтово совпадают на момент чтения, но это две копии.

Demo HashRouter содержит обзор/заказы/details/товары/остатки/цены/интеграции/add/automation/settings. 3 WB+5 Ozon+1 Yandex+1 MS accounts, 36 orders и chart series созданы массивами; fetch/localStorage в main.tsx отсутствуют. Buttons меняют memory state/toast/modals. Recharts/motion/Three.js procedural Hero3D — presentation, не operational evidence.

Landing демонстрирует более широкий SaaS, CTA ведёт в /demo/. ConnectionScene — animated SVG, не WebGL. FAQ widget не подтверждённый helpdesk. Offer/privacy/consent HTML явно draft/non-effective, placeholders; некоторые юридические тексты отстают от регистрации в current code.

## 16. Security/DevOps blockers

- .dockerignore не исключает .local/node_modules/dist, COPY . . может отправить sensitive local artifacts в build context.
- Один encryption key без явной rotation/key-id/restore процедуры.
- Нет подтверждённой production infra TLS/proxy/backups/PITR/alerting/key management.
- API требует direct loopback peer и exact public Host; deployment должен это учитывать.
- Worker priority publications/automatic может starve imports при постоянной очереди; client-local quota не решает multi-process pressure.
- No RLS; каждый новый route/SQL обязан сохранять tenant/actor invariants.
- Test fixture/integration artifacts должны быть исключены из production release.
- В CI показан Go/DB pipeline, нет полноценного frontend npm/E2E release gate.
- Нет billing/entitlements/подписки/отмены/arrears/webhook signature implementation.
- Нет завершённого consent/retention/export/delete/support operational lifecycle.

Это смесь confirmed gaps и inferred risks, не утверждение о доказанных remote exploits. Severity/evidence/концептуальные сценарии в 08.

## 17. Состояние проверок и готовность

В этом аудите ничего не запускалось, кроме read-only discovery и создания audit reports. Из предыдущего этапа: frontend 203 tests/typecheck/lint/build, Go tests/race/build/golangci passed; make lint с formatting diffs; полный integration с семью stock history/replay failures; один воспроизведён на HEAD. Новый removed-key/merchant regression и local register/email/verify/login/session read ранее прошли; worker выполнил mock task.

Это не зелёный релиз. Root cause всех failures неизвестен; нельзя исправить тесты ослаблением immutable history assertions. Нагрузка/RPS/coverage/prod readiness процент не измерялись.

Текущий вердикт: pre-commercial engineering prototype с содержательным MS/Ozon ядром. Для assisted pilot нужны явные границы и acceptance; для self-service paying clients не хватает production writes, billing, legal/ops и release verification.

## 18. Решения, которые следует сохранить

Контекст в I/O, bounded work, explicit transactions, no transaction across provider call, scopes и composite lineage, unknown≠zero, review-bound mappings, durable pre-dispatch intent, idempotency, per-target partial success, UNKNOWN/fences/readback/reconciliation, fail-closed gates и tenant actor guards. Не заменять эти свойства удобным auto-retry или public access ради демонстрации.

Не добавлять брокер/repository/microservices/JWT только по шаблону. Сначала решить product contract и измерить actual bottlenecks.

## 19. Рекомендуемая последовательность развития

P0: feature contract → root cause tests/CI → tenant/security/release/context hygiene → production mutation protocol/provider evidence → hosting/secrets/restore/monitoring → legal entity/docs/acceptance.
P1: billing+entitlements и assisted customer onboarding с несколькими accounts в узком MS/Ozon MVP.
P2: fairness/shared quotas, load/retention/indexes, recovery/support/privacy lifecycle, consistent design/docs.
P3: WB/Yandex, полноценные orders/returns/analytics/repricing и масштабирование по измерениям.

Подробные задачи с dependency, acceptance и S/M/L/XL — в 12. Не смешивать новый продуктовый roadmap с доказанными defects.

## 20. Открытые решения для владельца

Какой первый платный outcome? Какие tenants/targets разрешены для production writes? Multi-user или OWNER-only? Может ли external account принадлежать агентству и нескольким tenant? Как считать quotas и тариф? Где hosting/SMTP/backup/subprocessors? Кто принимает risk неизвестного provider outcome? Какие юридические условия и сроки хранения? Root causes тестов? Реальная пропускная способность?

Список 15; legal questions 11; API 13; exact schema 07; full data flows 14. Новый ChatGPT должен задавать эти вопросы до обещания полнофункционального SaaS и проверять actual code при следующих изменениях.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [go.mod:1](../../go.mod#L1) | configuration / declarations; see file |
| [cmd/api/main.go:1](../../cmd/api/main.go#L1) | 21: main; 27: run; 66: configuredHandler; 90: apiForMode |
| [cmd/worker/main.go:1](../../cmd/worker/main.go#L1) | 28: catalogClient; 85: moyskladCatalogClient; 142: main; 148: run |
| [internal/identity/merchant.go:1](../../internal/identity/merchant.go#L1) | 13: MerchantTenant |
| [internal/console/merchant_auth.go:1](../../internal/console/merchant_auth.go#L1) | 33: newMerchantAuth; 58: safe; 79: sameOrigin; 98: authorized; 133: readSession; 141: checkCSRF; 148: sessionFrom; 153: actorProvenance |
| [internal/console/merchant_scope.go:1](../../internal/console/merchant_scope.go#L1) | 18: authorizeScope; 47: scopeRequest; 72: isMappingReviewObject; 76: reviewWorkspace; 93: scopeMerchantBody; 125: merchantObject |
| [internal/console/auth.go:1](../../internal/console/auth.go#L1) | 33: loopbackHost; 42: ValidateAddress; 51: safe; 67: sameOrigin; 74: authorized; 121: logout |
| [internal/console/session_fixture_integration.go:1](../../internal/console/session_fixture_integration.go#L1) | 20: newAuth; 30: login; 67: Handler; 73: handlerWithConnections; 84: fixtureHandler |
| [internal/credentials/vault.go:1](../../internal/credentials/vault.go#L1) | 31: Valid; 34: validID; 50: Scope; 63: New; 80: FromEnvironment; 95: aad; 105: Encrypt; 122: Decrypt; 143: Digest; 161: Resolve; 168: ResolveWithRevision; 195: ScopeKey |
| [internal/catalogsync/runner.go:1](../../internal/catalogsync/runner.go#L1) | 21: RunNext; 54: dispatch; 87: write; 101: complete; 112: stop; 121: now; 128: validate; 154: corresponds; 169: containsCommand |
| [internal/infrastructure/ozon/catalog_write.go:1](../../internal/infrastructure/ozon/catalog_write.go#L1) | 20: Error; 21: Unwrap; 30: WriteCatalog; 64: validateCatalogCommand; 91: catalogWritePayload; 112: catalogPatchAttributes; 143: supportsTextAttribute; 153: validPatchText; 160: catalogTextValid; 164: catalogMutation; 203: readCatalogMutation; 217: catalogMutationBudget |
| [internal/config/stock_publication.go:1](../../internal/config/stock_publication.go#L1) | 13: StockPublicationTestEndpoint |
| [migrations/00039_merchant_accounts.sql:1](../../migrations/00039_merchant_accounts.sql#L1) | 6: saas_merchant_accounts |
| [migrations/00042_merchant_actor_expiry_fence.sql:1](../../migrations/00042_merchant_actor_expiry_fence.sql#L1) | configuration / declarations; see file |
| [web/src/App.tsx:1](../../web/src/App.tsx#L1) | 73: State; 80: Notice; 83: Loading; 86: Failure; 93: scopeParams; 101: SourceCard; 169: Overview; 235: Products; 245: StockRows; 315: WarehouseList; 449: Warehouses; 525: checkSummary |
| [E:/work/styk-demo/src/main.tsx:1](E:/work/styk-demo/src/main.tsx#L1) | 17: Brand; 18: Logo; 19: Status; 20: Card; 21: Button; 23: App; 28: PageHeader; 29: Empty; 30: Dashboard; 31: OrderTable; 32: Orders; 33: OrderDetails |
