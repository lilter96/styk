# Интеграции и внешний контракт

## Границы подтверждения

CONFIRMED здесь означает наличие клиента, allowlist, orchestration и persistence в коде. Поддержка конкретного provider account/permission, доступность endpoint сегодня, тариф API и реальные write semantics — UNVERIFIED в этом Read-Only аудите. Никаких внешних запросов не сделано. Старые live reports в docs — исторические свидетельства с ограниченным scope, не текущая валидация.

## Сводка

| Направление | Чтение | Запись | Коммерческая доступность |
|---|---|---|---|
| МойСклад → STYK | продукты/варианты/склады/stock/customerorders | локальное atomic persistence | код есть; runtime не перепроверен |
| Ozon → STYK | products/names/warehouses/FBS stock/postings/schema | локальное persistence | код есть; права/контракт перепроверить |
| МойСклад → STYK → Ozon | mapping/catalog plan + stock calculation | catalog и stock fixed test gates | PARTIAL, не общий production |
| Ozon → STYK → МойСклад | catalog authority/reverse command | ограниченный catalog writer | PARTIAL, не создание заказов |
| МойСклад → WB | отсутствует adapter | отсутствует | NOT IMPLEMENTED |
| МойСклад → Яндекс | отсутствует adapter | отсутствует | NOT IMPLEMENTED |
| WB/Яндекс → STYK → МойСклад | отсутствует | отсутствует | NOT IMPLEMENTED |

## МойСклад

Клиент: internal/infrastructure/moysklad/client.go; base API Remap 1.2 на официальном host. Bearer token в server-side client, ключ сохраняется encrypted и не включается в ordinary read responses. CatalogClient выделяет catalog-only permissions; он не writer документов, остатков, платежей, image upload или delete.

Основные чтения: entity/product, entity/variant, entity/store; report/stock/all; report/stock/bystore; entity/customerorder, detail и positions. Catalog paths включают productfolder/uom, product/variant metadata, attributes, companysettings/pricetype и exact entity references. Следует учитывать allowlist actual request builder, а не произвольный meta.href.

Pagination основана на bounded pages/offset. Дубликаты/невалидные identity и reference types отклоняются. МойСклад customerorders читает half-open updated window, millisecond alignment, не более 31 дня, с Moscow timezone. Deletion coverage, consistent point-in-time и полная видимость правами не доказаны.

stock, reserve, inTransit, quantity сохраняются как различные provider facts. Missing не превращается в zero; aggregation и warehouse observations не взаимозаменяемы. Завершённая traversal не является snapshot и может пропустить сущность, пересчитываемую provider.

Controlled catalog writer: create product/variant, update по ID, archive; текст, часть цен/баркодов/references/attributes. Expected revision и intent binding повторно проверяются. Конкретные поля зависят от type/capabilities. MS price payload не означает universal pricing engine.

## Ozon Seller API

Клиент internal/infrastructure/ozon/client.go. Authentication: Client-Id и Api-Key headers. Read POST methods не следует считать writes только по HTTP verb.

| Endpoint | Назначение |
|---|---|
| POST /v3/product/list | catalog identities, cursor/filter |
| POST /v3/product/info/list | names/product info |
| POST /v2/warehouse/list | warehouse identities/eligibility |
| POST /v2/product/info/stocks-by-warehouse/fbs | present/reserved/free_stock |
| POST /v4/posting/fbs/list | scoped time window posting traversal |
| POST /v3/posting/fbs/get | posting detail без запрошенных приватных financial/address extras |
| POST /v4/product/info/attributes | catalog attributes |
| POST /v1/product/info/description | description |
| POST /v1/description-category/tree | categories |
| POST /v1/description-category/attribute | schema |
| POST /v1/description-category/attribute/values | dictionary |
| POST /v1/product/import/info | asynchronous import status |
| POST /v3/product/import | restricted product creation |
| POST /v1/product/attributes/update | restricted update |
| POST /v2/products/stocks | fixed test/simulator stock writer |

Последние три mutation endpoints нельзя использовать через обычный read client. Catalog update validator допускает только known name/description; create требует полную валидную schema и возвращает task receipt, который затем poll/readback. Enum FieldPrice/FieldImages сам по себе не расширяет update allowlist.

Pagination: product last_id, warehouse cursor/has_next, bounded stock batches/pages. Separate IDs: product_id, offer_id, SKU, warehouse ID; они не заменяют друг друга. Ozon snapshot dedup и posting append rules отличаются: identical catalog batch может dedup, posting observations сохраняют observation history.

## Credentials, ownership и несколько кабинетов

credentials.Vault: AES-GCM на 32-byte key; AAD включает tenant/provider/account/revision. Resolver возвращает exact revision; 0 обозначает зарегистрированный environment fallback. DB connection ownership проверяется; global UNIQUE(source, account_id) предотвращает привязку одного внешнего аккаунта к разным tenant. Это продуктовая политика: совместное агентское использование одного кабинета не поддерживается автоматически.

Connection save/probe связывает фактическую external identity с выбранным scope, сохраняет request idempotency/HMAC digest. Disconnect disables record/revision; не удаляет исторические imports. При смене credentials pending frozen operations могут блокироваться как stale. Несколько аккаунтов одного provider представлены несколькими composite records, не отдельными services.

Автоматического OAuth refresh нет: это provider keys с ручной заменой. Rotation master encryption key не оформлена как самостоятельный workflow.

## Rate limiting / retry / budgets

Clients имеют per-instance интервал, backoff, attempts, HTTP/body/page/entity budgets и bounded Retry-After. 401/403 не являются безопасно повторяемыми ошибками. Read transport/429/5xx могут повторяться в рамках budget; слишком большой Retry-After приводит к безопасному отказу. Ozon post и MS pages не должны бесконечно обходить страницы.

Недостаток: limiter не общий между instances/processes. Несколько workers/console probes/каталожных клиентов могут делить один account quota без единой координации. Добавлять consumers без измерений нельзя.

Writes отправляются по durable intent, а не обычной read retry policy. Неизвестный outcome сохраняется и reconciles; blind resend недопустим. Per-target updated/errors и partial success обязательны; HTTP 200 не доказывает обработку каждого target.

## Monitoring/recovery

Есть machine-readable codes, counters/progress внутри операций, timestamps, Doctor, test worker presence, immutable evidence и readbacks. Нет подтверждённого dashboard/alert pipeline, provider quota telemetry или DLQ runbook. OUTCOME_UNKNOWN — бизнес-состояние, которое должно быть видно оператору; worker heartbeat не доказывает завершение удалённого запроса.

## Неподтверждённые внешние свойства

Provider write fencing, абсолютная полнота order/reserve coverage, формальная snapshot consistency, безопасная общая multi-client публикация и текущее состояние vendor contracts требуют отдельного разрешённого эксперимента. Не выводить production authority из тестового разрешения владельца.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/infrastructure/moysklad/client.go:1](../../internal/infrastructure/moysklad/client.go#L1) | 28: allowedReadPath; 56: Error; 57: New; 82: wait; 93: sleep; 106: retryAfter; 127: millisHeader; 138: rateDelay; 155: deferRequests; 163: get; 280: pages; 394: number |
| [internal/infrastructure/moysklad/customer_orders.go:1](../../internal/infrastructure/moysklad/customer_orders.go#L1) | 34: refID; 57: ReadCustomerOrders |
| [internal/infrastructure/moysklad/catalog.go:1](../../internal/infrastructure/moysklad/catalog.go#L1) | 47: allowedCatalogReadPath; 58: catalogLoopback; 63: catalogHostAllowed; 67: NewCatalogClient; 77: NewControlledTestCatalogClient; 89: validCatalogScope; 93: catalogTestEnabled; 97: scopeAllowed; 101: catalogAccount; 108: catalogIdentity; 115: verifyAccount; 126: catalogHTTP |
| [internal/infrastructure/moysklad/catalog_payload.go:1](../../internal/infrastructure/moysklad/catalog_payload.go#L1) | 16: validateCatalogCommand; 50: catalogSourceAllowed; 58: catalogPayload; 87: catalogPayloadField; 117: catalogTextValue; 128: catalogCreatePayload; 145: catalogPayloadExtensions; 170: validateCatalogCharacteristics; 178: catalogReferencePayload; 191: catalogPricePayload; 200: catalogPriceIdentity; 220: catalogBarcodePayload |
| [internal/infrastructure/ozon/client.go:1](../../internal/infrastructure/ozon/client.go#L1) | 62: NewControlledTestStockClient; 100: Error; 106: New; 148: sleep; 161: wait; 172: retryAfter; 195: post; 326: observeCatalogRequest; 337: UnmarshalJSON; 346: id; 393: nextCursor; 406: Read |
| [internal/infrastructure/ozon/postings.go:1](../../internal/infrastructure/ozon/postings.go#L1) | 41: ReadPostings |
| [internal/infrastructure/ozon/catalog_schema.go:1](../../internal/infrastructure/ozon/catalog_schema.go#L1) | 39: UnmarshalJSON; 71: UnmarshalJSON; 123: catalogReadPath; 132: CategoryTree; 145: CategoryAttributes; 168: AttributeValues; 196: decodeCatalogJSON; 206: decodeCatalogFacts |
| [internal/infrastructure/ozon/catalog_write.go:1](../../internal/infrastructure/ozon/catalog_write.go#L1) | 20: Error; 21: Unwrap; 30: WriteCatalog; 64: validateCatalogCommand; 91: catalogWritePayload; 112: catalogPatchAttributes; 143: supportsTextAttribute; 153: validPatchText; 160: catalogTextValid; 164: catalogMutation; 203: readCatalogMutation; 217: catalogMutationBudget |
| [internal/credentials/vault.go:1](../../internal/credentials/vault.go#L1) | 31: Valid; 34: validID; 50: Scope; 63: New; 80: FromEnvironment; 95: aad; 105: Encrypt; 122: Decrypt; 143: Digest; 161: Resolve; 168: ResolveWithRevision; 195: ScopeKey |
| [internal/catalogprovider/factory.go:1](../../internal/catalogprovider/factory.go#L1) | 26: ResolveCatalog; 47: client; 58: moysklad; 77: ozon; 96: defaultMoysklad; 100: defaultOzon |
