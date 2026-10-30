# Продукт и функциональная матрица

## Назначение

PLANNED, из задания владельца: МойСклад ↔ STYK ↔ Wildberries/Ozon/Яндекс Маркет для продавцов России и Беларуси; несколько кабинетов каждого маркетплейса; ориентир подписки 5 000 RUB/месяц; предполагаемое белорусское ООО с двумя участниками 50/50. Код не подтверждает действующее юрлицо, договор, тариф или оплату.

CONFIRMED: текущий технический продукт концентрируется на МойСклад/Ozon, безопасном чтении наблюдений, reviewed mappings и ограниченном каталожном/stock эксперименте. Источники: internal/infrastructure/{moysklad,ozon}, internal/console/http.go, internal/catalogsync, internal/stocksync.

## Обозначения

IMPLEMENTED = присутствует прослеживаемая реализация в коде, а не гарантия успешного исполнения сейчас. PARTIAL = цепочка или продуктовый охват ограничены. MOCK = массивы/состояние/симулятор. NOT IMPLEMENTED = не найдено в просмотренном коде и инвентаризации. UNVERIFIED = необходим runtime/внешний контракт. BROKEN = установленный дефект, с указанием происхождения доказательства.

CONFIRMED — прямой факт кода; INFERRED — вывод; UNKNOWN — нет данных; PLANNED — требование/описание. Не смешивать эти две классификации.

## Пользователи и SaaS

| Модуль | Функция | Статус | Backend | Frontend | Доказательства | Что осталось |
|---|---|---|---|---|---|---|
| Identity | Регистрация | IMPLEMENTED | RegisterPending, транзакция user/workspace/binding | MerchantAuth register | identity/users.go; console/merchant_public.go | Проверить SMTP и abuse в production |
| Identity | Проверка почты, выбор пароля | IMPLEMENTED | verification token digest, expiry | #verify=token | identity/verification.go; MerchantAuth.tsx | SMTP delivery/retry наблюдаемость |
| Identity | Email/password login | IMPLEMENTED | Authenticate/CreateSession | MerchantAuth | identity/users.go, sessions.go | Deployment/TLS проверка |
| Identity | Секретный общий ключ | NOT IMPLEMENTED в обычном runtime | local mode отклоняется | форма отсутствует | cmd/api/main.go; App.tsx | Удалить остаточный test/legacy долг отдельной задачей |
| Identity | Восстановление | IMPLEMENTED | reset token, auth_version | #reset=token | identity/reset.go | Проверить delivery/revocation сценарии |
| Identity | JWT/refresh/OAuth/MFA | NOT IMPLEMENTED | используются opaque sessions | нет | identity/sessions.go; merchant_routes.go | Решение о необходимости, не обязательная замена cookies |
| Организации | Tenant/workspace | PARTIAL | один merchant binding/user | выбранные source accounts | identity/merchant.go; migration 39 | Команды/организации/приглашения |
| Доступ | Роли | PARTIAL | схема OWNER/ADMIN/OPERATOR/VIEWER; текущий merchant OWNER-only | нет управления командой | migration 33, 39, 42 | Согласовать настоящую RBAC-модель |
| SaaS | Тарифы/подписка/оплата/отмена/просрочка | NOT IMPLEMENTED | нет billing tables/routes | демо/лендинг | API и migration inventory | Полный billing lifecycle |
| SaaS | Entitlements/лимиты тарифа | NOT IMPLEMENTED | технические budgets ≠ тарифы | нет | config и import limits | Product quotas |
| SaaS | Billing webhook | NOT IMPLEMENTED | нет endpoint/signature handler | нет | route inventory | Idempotency, подписи, ledger |
| Privacy | Удаление аккаунта/экспорт | NOT IMPLEMENTED | публичных workflows нет | нет | merchant_routes.go | Retention и запросы субъектов |
| Право | Принятие документов | NOT IMPLEMENTED | нет acceptance ledger | draft legal pages | соседний landing/public/legal | Версии/дата/субъект принятия |

## Интеграции и товарный каталог

| Модуль | Функция | Статус | Backend | Frontend | Доказательства | Что осталось |
|---|---|---|---|---|---|---|
| МойСклад | Проверка и хранение токена | IMPLEMENTED | probe, Encrypt, save | CredentialEditor | console/credentials.go; credentials/vault.go | Production key lifecycle |
| Ozon | Client-ID/API-Key | IMPLEMENTED | probe, revisioned encrypted record | CredentialEditor | console/credentials.go; ozon/client.go | Проверка прав в конкретном аккаунте |
| WB | Подключение/синхронизация | NOT IMPLEMENTED | адаптера нет | только бренд/демо | infrastructure inventory | Контракт и вертикальный срез |
| Яндекс | Подключение/синхронизация | NOT IMPLEMENTED | адаптера нет | бренд/демо | infrastructure inventory | Контракт и вертикальный срез |
| Интеграции | Несколько кабинетов | PARTIAL | composite scope tenant/provider/account | selector/account list | migration 23; WorkspaceSwitcher | Полный multi-account E2E и UX |
| Интеграции | Disconnect/замена токена | IMPLEMENTED | enabled/revision, ownership | editor/dialog | postgres/credentials.go | Проверить in-flight и recovery |
| Интеграции | OAuth refresh токенов | NOT IMPLEMENTED | ручные provider keys | ручной ввод | credentials/vault.go | Не выдумывать provider OAuth |
| Товары | Импорт товаров/вариантов | IMPLEMENTED | МойСклад + Ozon bounded readers | imports + products | application/import.go, ozon_import.go | Runtime/права/масштаб |
| Товары | SKU/article/code matching | IMPLEMENTED | deterministic candidates, explicit confirm | ProductMappingPage | matching; console/mapping_confirmation.go | Отдельная проверка физической идентичности |
| Товары | История mapping | IMPLEMENTED | review, audit, idempotency | history/review UI | migrations 17–20 | Replacement/revocation охват |
| Каталог | Source authority/conflicts | IMPLEMENTED как алгоритм | BuildPlan + baseline | CatalogSync | catalog/plan.go | Валидировать каждый provider field |
| Каталог | Создание/обновление | PARTIAL | controlled create/update writer | preview/approval | catalogsync/runner.go | Production rollout |
| Каталог | Категории/attributes | PARTIAL | Ozon schema/dictionaries; MS metadata | schema editor | catalog_schema.go; catalog_metadata.go | Не все поля writable |
| Каталог | Изображения | PARTIAL | поле/чтение/verify ограничения | ограничения в preview | catalog/types.go; adapters | Нет общего image upload pipeline |
| Цены | Чтение/отдельные catalog payloads | PARTIAL | price field; MS salePrices; Ozon creation | catalog, без /pricing | catalog_payload.go; ozon/catalog_write.go | Цена в DTO ≠ массовый repricing |
| Цены | Массовое обновление/наценки/комиссии | MOCK / NOT IMPLEMENTED runtime | нет отдельного сервиса | Pricing в demo | demo/src/main.tsx | Entitlements, review, safeguards, история |

## Остатки, заказы и аналитика

| Модуль | Функция | Статус | Backend | Frontend | Доказательства | Что осталось |
|---|---|---|---|---|---|---|
| Остатки | Наблюдения по складам | IMPLEMENTED | source + Ozon FBS tables | warehouses/stock views | migrations 3,4; console/http.go GET /api/stock | Наблюдения ≠ текущая истина |
| Остатки | Сопоставление складов | IMPLEMENTED | review-bound confirmation | WarehouseReview | migrations 19,20 | Семантика физического склада |
| Остатки | Расчёт безопасного количества | PARTIAL | evidence/proofs/policy | shadow/preview controls | commitment/calculator.go | Не хватает provider guarantees |
| Остатки | Публикация произвольным клиентам | NOT IMPLEMENTED / held | production gate закрыт | eligibility pending | config/stock_publication.go; stock_sync.go | Отдельный production design |
| Остатки | Тестовая запись Ozon | PARTIAL | fixed allowlist/simulator | test controls | stocksync; testpublication | Не масштабировать разрешение |
| Остатки | Резервирование в МойСклад | NOT IMPLEMENTED | reserve читается, документы не создаются | нет | moysklad/customer_orders.go | Внешние обязательства и reconciliation |
| Остатки | Автоматический цикл | PARTIAL | outbox/config/cycle/test scope | automatic controls | migrations 27–32 | Fairness, production permissions |
| Заказы | Ozon posting list/detail | IMPLEMENTED как read evidence | v4 list/v3 get | order evidence, не order cabinet | ozon/postings.go | Полный workflow заказов |
| Заказы | MS customerorders/positions | IMPLEMENTED как read evidence | bounded time windows | refresh evidence | moysklad/customer_orders.go | Deletion/coverage unknown |
| Заказы | Correlation/dedup identity | PARTIAL | scoped identity + evidence | диагностические результаты | ordercorrelation; migrations 12,15 | Не создавать заказ по heuristic |
| Заказы | Создание в МойСклад | NOT IMPLEMENTED | нет production document writer | demo only | MS catalog writer excludes documents | Idempotency/unknown/reconciliation |
| Заказы | Сборка/отгрузка/отмена/этикетки/возвраты | NOT IMPLEMENTED runtime | нет endpoint chains | demo state/modals | route inventory; demo functions | Реальные provider workflows |
| Заказы | Синхронизация статусов | NOT IMPLEMENTED | наблюдения статусов ≠ запись | demo statuses | postings/customer_orders | Transition model/ownership |
| Аналитика | Выручка/прибыль/расходы/комиссии | MOCK | отсутствует financial model | demo Recharts | demo chartData/seedOrders | Источники, ledger, правила |
| Аналитика | Сравнение/агрегация аккаунтов | MOCK | нет production aggregation | фильтры demo | demo Dashboard | Реальные scoped aggregates |
| Эксплуатация | Health/Doctor | IMPLEMENTED | health + scoped doctor | diagnostics | transport/http.go; doctor | Metrics/alerts/runbooks |

*Точное размещение обработчика остатков см. сгенерированный API inventory в 13; названия таблиц и migration provenance — в 07.

## Продуктовая граница

Связка «получить наблюдения → подтвердить соответствие → увидеть ограничения» существенно ближе к реализации, чем «полностью автоматизировать продажи». Демо не является тестом рабочей системы. Нельзя обещать paid subscription, WB/Yandex, прибыль, fulfillment или безопасную production-публикацию только по наличию дизайна.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/identity/users.go:1](../../internal/identity/users.go#L1) | 14: Register; 40: RegisterPending; 48: insertRegistration; 75: insertWorkspace; 85: event; 95: RecordAction; 109: Authenticate; 134: AllowAuthAttempt |
| [internal/identity/merchant.go:1](../../internal/identity/merchant.go#L1) | 13: MerchantTenant |
| [internal/console/merchant_routes.go:1](../../internal/console/merchant_routes.go#L1) | 5: registerMerchantRoutes |
| [internal/console/credentials.go:1](../../internal/console/credentials.go#L1) | 26: registerCredentialRoutes; 35: newMoy; 41: newOzon; 47: resolveOzon; 73: resolveMoy; 100: credentialError; 114: get; 161: save; 228: writeCredentialResult; 234: disconnect; 268: scopedCheckKey; 272: probeReason |
| [internal/console/stock_sync.go:1](../../internal/console/stock_sync.go#L1) | 23: registerStockSyncRoutes; 38: eligibility; 67: automaticStatus; 81: automaticConfigure; 99: automaticRun; 121: shadow; 261: syncScope; 265: stockSyncError; 281: decodeStockSync; 290: get; 303: preview; 327: enable |
| [internal/catalog/types.go:1](../../internal/catalog/types.go#L1) | 203: Text; 214: Empty; 216: Clear |
| [internal/infrastructure/ozon/catalog_write.go:1](../../internal/infrastructure/ozon/catalog_write.go#L1) | 20: Error; 21: Unwrap; 30: WriteCatalog; 64: validateCatalogCommand; 91: catalogWritePayload; 112: catalogPatchAttributes; 143: supportsTextAttribute; 153: validPatchText; 160: catalogTextValid; 164: catalogMutation; 203: readCatalogMutation; 217: catalogMutationBudget |
| [internal/infrastructure/moysklad/catalog_payload.go:1](../../internal/infrastructure/moysklad/catalog_payload.go#L1) | 16: validateCatalogCommand; 50: catalogSourceAllowed; 58: catalogPayload; 87: catalogPayloadField; 117: catalogTextValue; 128: catalogCreatePayload; 145: catalogPayloadExtensions; 170: validateCatalogCharacteristics; 178: catalogReferencePayload; 191: catalogPricePayload; 200: catalogPriceIdentity; 220: catalogBarcodePayload |
| [web/src/App.tsx:1](../../web/src/App.tsx#L1) | 73: State; 80: Notice; 83: Loading; 86: Failure; 93: scopeParams; 101: SourceCard; 169: Overview; 235: Products; 245: StockRows; 315: WarehouseList; 449: Warehouses; 525: checkSummary |
| [E:/work/styk-demo/src/main.tsx:1](E:/work/styk-demo/src/main.tsx#L1) | 17: Brand; 18: Logo; 19: Status; 20: Card; 21: Button; 23: App; 28: PageHeader; 29: Empty; 30: Dashboard; 31: OrderTable; 32: Orders; 33: OrderDetails |
