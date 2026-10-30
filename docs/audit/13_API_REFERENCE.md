# API reference: статическая карта

## Контракт и ограничения

Источники: actual ServeMux registrations в internal/console и internal/transport, DTO structs, JSON tags, frontend api wrappers. OpenAPI schema/generator в repo не обнаружен. Это карта текущих handler registrations, а не обещание стабильной версии публичного API или результат вызова endpoints.

Префикс /api подключается cmd/api/main.go только в authenticated merchant mode. disabled mode содержит только health. local auth mode удалён из normal bootstrap. Test fixtures могут регистрировать свои session endpoints, но они не входят в таблицу normal runtime.

## Общие правила

- GET health/live и health/ready публичны; live проверяет процесс, ready — DB ping.
- POST registration/verification/reset/session — public auth flows с exact Origin/Host/Content-Type, rate limiting и strict JSON.
- Все business endpoints используют a.authorized; merchant middleware проверяет cookie, session, CSRF, tenant binding и object scope.
- Cookie: styk_session, opaque; не JWT. CSRF header: X-STYK-CSRF для authenticated mutations.
- tenant браузера не является authority: inject own tenant либо reject mismatch. Account pair всё равно проверяется отдельно.
- HTTP ошибки включают JSON error code; frontend ApiError sanitizes response. 401 unauthenticated, 403 boundary/CSRF, 404 unavailable scope/object, 409 stale/idempotency/conflict, 503 unavailable/unknown persistence. Для точного конкретного endpoint смотреть handler.
- Idempotency часто поле body, не универсальный HTTP Idempotency-Key header. Не придумывать header без проверки DTO.
- GET не должен запускать provider writes. POST /order-evidence/refresh делает provider reads и durable local writes; это не read-only операция для аудита.
- 204 имеет пустое тело; frontend api/post возвращает null. Не делать JSON.parse для 204.

## Authentication

| Method/path | Body / смысл | Ответ/заметки |
|---|---|---|
| GET /api/auth-mode | mode discovery | merchant в normal runtime |
| POST /api/registration | email; имя/регистрационные поля см. handler | 202, verification delivery; не login |
| POST /api/verification/request | email | 202, rate-limited resend |
| POST /api/verification | token + password | 204; подтверждает ownership и задаёт пароль |
| POST /api/session | email + password | 204 + cookie |
| GET /api/session | authenticated cookie | user/account_id/csrf/expires_at |
| DELETE /api/session | cookie + Origin + CSRF | 204 + revoke/clear |
| POST /api/password-reset/request | email | 202 |
| POST /api/password-reset | token + password | пароль/version/session последствия в identity/reset.go |

POST /api/session с secret вместо email/password не является оставленным login mechanism: public decoder rejects unknown field. Старый endpoint path переиспользован действующим способом входа и не должен удаляться целиком.

## Business DTO families

- Imports: scope tenant + provider account, operation type, idempotency key; operation states QUEUED/RUNNING/SUCCEEDED/FAILED/CANCELLED/OUTCOME_UNKNOWN; progress/attempt/lease/outcome fields.
- Credentials: provider/account/name/secret input + expected revision/request identity; read metadata не plaintext key. Disconnect — отдельная mutation.
- Product/warehouse mapping: scope pair, selected IDs, review ID, acknowledgement/idempotency; review freezes revisions/session. Confirm не доверяет new arbitrary identity.
- Stock sync: domain.StockSyncScope, preview/settings/review IDs и mutation identity; eligibility может вернуть awaiting production activation.
- Catalog config: Config scope/revision/enabled/test_enabled/interval/rules/targets; SaveConfigRequest expected_revision/idempotency_key; actor только server-side.
- Catalog preview: scope + expected_revision + enrollment_ids + manual_values.
- Catalog approve: scope + preview_id + enrollment_ids + idempotency_key; выбирает reviewed whole products.
- Catalog reconcile: readback/review-specific input; смотреть catalog reconcile DTO, не считать это «повторить write».
- Lists: bounded pages/cursors/search; account IDs, offer_id и SKU — разные namespaces. Cursor может pin revision, не менять source context в середине списка.

Точные JSON поля, необязательность и ограничения определяются исходным struct + validator. Для интеграции клиента желательно добавить формальную schema в отдельной задаче, а не копировать этот текст как machine contract.

## Полный normal route inventory

Таблица ниже извлечена из registrations с исключением *_test.go и session_fixture*.go. Указаны source line и handler expression. Для динамического handler expression описания выше объясняют domain family. Наличие route не означает, что scope gate разрешит действие обычному клиенту.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/console/http.go:1](../../internal/console/http.go#L1) | 20: MerchantHandler; 31: handlerWithAuth; 152: writeJSON; 156: writeError |
| [internal/console/merchant_routes.go:1](../../internal/console/merchant_routes.go#L1) | 5: registerMerchantRoutes |
| [internal/console/credentials.go:1](../../internal/console/credentials.go#L1) | 26: registerCredentialRoutes; 35: newMoy; 41: newOzon; 47: resolveOzon; 73: resolveMoy; 100: credentialError; 114: get; 161: save; 228: writeCredentialResult; 234: disconnect; 268: scopedCheckKey; 272: probeReason |
| [internal/console/catalog_sync.go:1](../../internal/console/catalog_sync.go#L1) | 26: registerCatalogRoutes; 41: catalogFactory; 52: catalogLiveAllowed; 56: allowed; 59: catalogScope; 63: catalogActor; 74: catalogScope; 78: config; 97: catalogCapabilities; 110: saveConfig; 144: preview; 162: approve |
| [internal/console/stock_sync.go:1](../../internal/console/stock_sync.go#L1) | 23: registerStockSyncRoutes; 38: eligibility; 67: automaticStatus; 81: automaticConfigure; 99: automaticRun; 121: shadow; 261: syncScope; 265: stockSyncError; 281: decodeStockSync; 290: get; 303: preview; 327: enable |
| [internal/console/order_evidence.go:1](../../internal/console/order_evidence.go#L1) | 56: registerOrderEvidenceRoutes; 62: get; 84: refresh; 111: runOrderRefresh; 141: orderRefreshError; 155: orderReaders; 183: orderCredential; 209: orderReaderError; 221: orderEvidenceLockKey; 227: lockOrderEvidence; 252: closeOrderEvidenceSession; 261: refreshSourceOrders |
| [internal/console/merchant_scope.go:1](../../internal/console/merchant_scope.go#L1) | 18: authorizeScope; 47: scopeRequest; 72: isMappingReviewObject; 76: reviewWorkspace; 93: scopeMerchantBody; 125: merchantObject |
| [internal/transport/http.go:1](../../internal/transport/http.go#L1) | 10: Handler |
| [web/src/api.ts:1](../../web/src/api.ts#L1) | 4: setCSRF; 179: createImportIntent; 287: createConfirmationIntent |

| Method | Route | Source line | Handler |
|---|---|---|---|
| GET | `/api/catalog-sync/config` | [internal/console/catalog_sync.go:30](../../internal/console/catalog_sync.go#L30) | `a.authorized(api.audit(api.config)))` |
| POST | `/api/catalog-sync/config` | [internal/console/catalog_sync.go:31](../../internal/console/catalog_sync.go#L31) | `a.authorized(a.connectionMutation(api.audit(api.saveConfig))))` |
| GET | `/api/catalog-sync/products` | [internal/console/catalog_sync.go:32](../../internal/console/catalog_sync.go#L32) | `a.authorized(api.audit(api.products)))` |
| POST | `/api/catalog-sync/preview` | [internal/console/catalog_sync.go:33](../../internal/console/catalog_sync.go#L33) | `a.authorized(a.connectionMutation(api.audit(api.preview))))` |
| POST | `/api/catalog-sync/reconcile` | [internal/console/catalog_sync.go:34](../../internal/console/catalog_sync.go#L34) | `a.authorized(a.connectionMutation(api.audit(api.reconcile))))` |
| POST | `/api/catalog-sync/approve` | [internal/console/catalog_sync.go:35](../../internal/console/catalog_sync.go#L35) | `a.authorized(a.connectionMutation(api.audit(api.approve))))` |
| GET | `/api/catalog-sync/operations` | [internal/console/catalog_sync.go:36](../../internal/console/catalog_sync.go#L36) | `a.authorized(api.audit(api.operations)))` |
| GET | `/api/catalog-sync/categories` | [internal/console/catalog_sync.go:37](../../internal/console/catalog_sync.go#L37) | `a.authorized(api.audit(api.categories)))` |
| GET | `/api/catalog-sync/attributes` | [internal/console/catalog_sync.go:38](../../internal/console/catalog_sync.go#L38) | `a.authorized(api.audit(api.attributes)))` |
| GET | `/api/integrations/{provider}/credentials` | [internal/console/credentials.go:31](../../internal/console/credentials.go#L31) | `a.authorized(api.get))` |
| POST | `/api/integrations/{provider}/credentials` | [internal/console/credentials.go:32](../../internal/console/credentials.go#L32) | `a.authorized(a.connectionMutation(api.save)))` |
| POST | `/api/integrations/{provider}/disconnect` | [internal/console/credentials.go:33](../../internal/console/credentials.go#L33) | `a.authorized(a.connectionMutation(api.disconnect)))` |
| GET | `/api/auth-mode` | [internal/console/http.go:52](../../internal/console/http.go#L52) | `func(w http.ResponseWriter, r *http.Request) {` |
| POST | `/api/imports` | [internal/console/http.go:65](../../internal/console/http.go#L65) | `a.authorized(a.connectionMutation(imports.create)))` |
| GET | `/api/imports` | [internal/console/http.go:66](../../internal/console/http.go#L66) | `a.authorized(imports.list))` |
| GET | `/api/imports/{id}` | [internal/console/http.go:67](../../internal/console/http.go#L67) | `a.authorized(imports.get))` |
| POST | `/api/imports/{id}/cancel` | [internal/console/http.go:68](../../internal/console/http.go#L68) | `a.authorized(a.connectionMutation(imports.cancel)))` |
| GET | `/api/integrations` | [internal/console/http.go:69](../../internal/console/http.go#L69) | `a.authorized(connections.list))` |
| POST | `/api/integrations/{provider}/probe` | [internal/console/http.go:70](../../internal/console/http.go#L70) | `a.authorized(a.connectionMutation(connections.probe)))` |
| POST | `/api/integrations/{provider}/register` | [internal/console/http.go:71](../../internal/console/http.go#L71) | `a.authorized(a.connectionMutation(connections.register)))` |
| GET | `/api/registrations` | [internal/console/http.go:72](../../internal/console/http.go#L72) | `a.authorized(func(w http.ResponseWriter, r *http.Request) {` |
| GET | `/api/diagnostics` | [internal/console/http.go:124](../../internal/console/http.go#L124) | `a.authorized(func(w http.ResponseWriter, r *http.Request) {` |
| POST | `/api/product-mapping-reviews` | [internal/console/http.go:138](../../internal/console/http.go#L138) | `a.authorized(a.createMappingReview(db, lifetime)))` |
| POST | `/api/product-mapping-reviews/{id}/confirm` | [internal/console/http.go:139](../../internal/console/http.go#L139) | `a.authorized(a.confirmMappingReview(db)))` |
| GET | `/api/product-mapping-reviews/{id}` | [internal/console/http.go:140](../../internal/console/http.go#L140) | `a.authorized(a.inspectMappingReview(db)))` |
| GET | `/api/products` | [internal/console/http.go:141](../../internal/console/http.go#L141) | `a.authorized(products(db)))` |
| GET | `/api/moysklad-products` | [internal/console/http.go:142](../../internal/console/http.go#L142) | `a.authorized(searchTargets(db)))` |
| GET | `/api/product-mapping-history` | [internal/console/http.go:143](../../internal/console/http.go#L143) | `a.authorized(mappingHistory(db)))` |
| POST | `/api/warehouse-mapping-reviews` | [internal/console/http.go:144](../../internal/console/http.go#L144) | `a.authorized(a.createWarehouseReview(db, lifetime)))` |
| POST | `/api/warehouse-mapping-reviews/{id}/confirm` | [internal/console/http.go:145](../../internal/console/http.go#L145) | `a.authorized(a.confirmWarehouseReview(db)))` |
| GET | `/api/warehouse-mapping-reviews/{id}` | [internal/console/http.go:146](../../internal/console/http.go#L146) | `a.authorized(a.inspectWarehouseReview(db)))` |
| GET | `/api/warehouse-mapping-review` | [internal/console/http.go:147](../../internal/console/http.go#L147) | `a.authorized(warehouseReview(db)))` |
| GET | `/api/warehouses` | [internal/console/http.go:148](../../internal/console/http.go#L148) | `a.authorized(warehouses(db)))` |
| GET | `/api/stock` | [internal/console/http.go:149](../../internal/console/http.go#L149) | `a.authorized(stock(db)))` |
| POST | `/api/registration` | [internal/console/merchant_routes.go:7](../../internal/console/merchant_routes.go#L7) | `m.register)` |
| POST | `/api/verification/request` | [internal/console/merchant_routes.go:8](../../internal/console/merchant_routes.go#L8) | `m.resendVerification)` |
| POST | `/api/verification` | [internal/console/merchant_routes.go:9](../../internal/console/merchant_routes.go#L9) | `m.verify)` |
| POST | `/api/password-reset/request` | [internal/console/merchant_routes.go:10](../../internal/console/merchant_routes.go#L10) | `m.requestReset)` |
| POST | `/api/password-reset` | [internal/console/merchant_routes.go:11](../../internal/console/merchant_routes.go#L11) | `m.reset)` |
| POST | `/api/session` | [internal/console/merchant_routes.go:12](../../internal/console/merchant_routes.go#L12) | `m.login)` |
| GET | `/api/session` | [internal/console/merchant_routes.go:13](../../internal/console/merchant_routes.go#L13) | `a.authorized(m.session))` |
| DELETE | `/api/session` | [internal/console/merchant_routes.go:14](../../internal/console/merchant_routes.go#L14) | `a.authorized(m.logout))` |
| GET | `/api/stock-sync/order-evidence` | [internal/console/order_evidence.go:58](../../internal/console/order_evidence.go#L58) | `a.authorized(api.get))` |
| POST | `/api/stock-sync/order-evidence/refresh` | [internal/console/order_evidence.go:59](../../internal/console/order_evidence.go#L59) | `a.authorized(a.connectionMutation(api.refresh)))` |
| GET | `/api/stock-sync` | [internal/console/stock_sync.go:25](../../internal/console/stock_sync.go#L25) | `a.authorized(api.get))` |
| GET | `/api/stock-sync/eligibility` | [internal/console/stock_sync.go:26](../../internal/console/stock_sync.go#L26) | `a.authorized(api.eligibility))` |
| GET | `/api/stock-sync/shadow` | [internal/console/stock_sync.go:27](../../internal/console/stock_sync.go#L27) | `a.authorized(api.shadow))` |
| POST | `/api/stock-sync/preview` | [internal/console/stock_sync.go:28](../../internal/console/stock_sync.go#L28) | `a.authorized(a.connectionMutation(api.preview)))` |
| POST | `/api/stock-sync/enable` | [internal/console/stock_sync.go:29](../../internal/console/stock_sync.go#L29) | `a.authorized(a.connectionMutation(api.enable)))` |
| POST | `/api/stock-sync/disable` | [internal/console/stock_sync.go:30](../../internal/console/stock_sync.go#L30) | `a.authorized(a.connectionMutation(api.disable)))` |
| POST | `/api/stock-sync/operations/{id}/reconcile` | [internal/console/stock_sync.go:31](../../internal/console/stock_sync.go#L31) | `a.authorized(a.connectionMutation(api.reconcile)))` |
| POST | `/api/stock-sync/operations/{id}/supersede` | [internal/console/stock_sync.go:32](../../internal/console/stock_sync.go#L32) | `a.authorized(a.connectionMutation(api.supersede)))` |
| GET | `/api/stock-sync/automatic` | [internal/console/stock_sync.go:33](../../internal/console/stock_sync.go#L33) | `a.authorized(api.automaticStatus))` |
| POST | `/api/stock-sync/automatic` | [internal/console/stock_sync.go:34](../../internal/console/stock_sync.go#L34) | `a.authorized(a.connectionMutation(api.automaticConfigure)))` |
| POST | `/api/stock-sync/automatic/run` | [internal/console/stock_sync.go:35](../../internal/console/stock_sync.go#L35) | `a.authorized(a.connectionMutation(api.automaticRun)))` |
| GET | `/health/live` | [internal/transport/http.go:12](../../internal/transport/http.go#L12) | `func(w http.ResponseWriter, r *http.Request) {` |
| GET | `/health/ready` | [internal/transport/http.go:16](../../internal/transport/http.go#L16) | `func(w http.ResponseWriter, r *http.Request) {` |

## Additional exact DTO notes

Registration accepts email and legacy password/workspace fields, but the latter two are intentionally ignored by merchantAuth.register; RegisterPending uses an internal workspace label, and the verified mailbox holder sets the usable password. Do not build a team/workspace creation UX from these ignored fields.

Catalog ReconciliationRequest (internal/catalogsync/manual_reconciliation.go:14): scope, work_id, reason, idempotency_key, acknowledge, accept_warnings; actor is server-only. It authorizes reads of an already dispatched task, not another write or supersession.
