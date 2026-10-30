# Frontend: рабочая консоль, демо и лендинг

## Рабочая консоль

web/package.json: React 19, TypeScript 5.9, Vite 6, react-router-dom 7, TanStack Query 5. main.tsx создаёт один QueryClient: retry=false, staleTime=30s, refetchOnWindowFocus=false. BrowserRouter требует SPA fallback в production host. Vite development proxy отправляет /api на loopback API с сохранением Host.

State: server state в Query; scopes/account selection/session/form/dialog state в React; некоторые sessionStorage keys для выбора аккаунтов. Redux/Zustand не найдены. authFetch хранит CSRF только в памяти, добавляет X-STYK-CSRF на mutations, сообщает styk-session-expired. Cookie HttpOnly не читается JavaScript.

## Страницы

| Route | Компоненты | API и функции | Данные и полнота |
|---|---|---|---|
| / (неавторизованный) | MerchantAuth | registration/session/verification/reset | реальная auth цепочка, runtime зависит от SMTP |
| #verify=token / #reset=token | MerchantAuth screens | POST verification/password-reset | fragment token flow, не отдельные server routes |
| / (после входа) | App Overview/SourceCard | registrations, imports, stock/doctor summaries | persisted observations; не financial dashboard |
| /integrations | Integrations, CredentialEditor, ImportSubmission, StockSyncControls | credentials/probe/register/imports/stock-sync/order-evidence | реальные handlers, publication ограничена gates |
| /products | ProductMappingPage, mapping dialogs | products, moysklad-products, reviews/confirm/history | кандидат/confirmation, не общий CMS карточек |
| /warehouses | WarehouseReview | warehouses/stock/review/history-confirm chain | observations + explicit mapping, missing≠zero |
| /catalog-sync | lazy CatalogSync, CatalogSyncConfig/CatalogSyncAttribute/CatalogSyncPreview | config/products/categories/attributes/preview/approve/operations/reconcile | реальный restricted workflow; не общий production sync |
| /diagnostics | App diagnostics view | GET diagnostics | scoped Doctor, не financial analytics |

Точное множество routes задано App.tsx Routes. /orders, /inventory, /pricing, /analytics, /automation, /settings в реальной console как полноценные страницы не найдены. Отдельные embedded controls не считать наличием этих модулей.

## Forms/UX/error handling

MerchantAuth: login/register/forgot/resend/verify/reset screens; required/email/password HTML validation и обработка API codes; server остаётся authority. Dialog/form state сохраняется при некоторых session refreshes. Query cache и selected account state сбрасываются при смене/потере session; это важно для tenant safety.

Mutation hooks в Confirmation, ImportSubmission, CredentialSubmission и StockSyncSubmission отделяют pending intent/unknown outcome от обычного error. Нельзя закрывать неопределённый outcome кнопкой нового submit без reconciliation. AbortSignal передаётся для queries. Таблицы используют обычный DOM/CSS, ограниченные страницы/поиск; специализированной virtualization библиотеки нет.

AppShell/UI/IntegrationDialog дают sidebar/mobile drawer, semantic states, focus/keyboard affordances и общий вид. Это локальный UI-kit, не внешний enterprise component framework. CSS каскад: style → imports → mapping → design-system, плюс catalog styles. Поздние overrides и повторные login/table селекторы создают regression risk. Responsive задан @media; accessibility и реальное поведение на устройствах в этом аудите не тестировались.

## Standalone demo и встроенная копия

E:/work/styk-demo/src/main.tsx и E:/work/styk-landing/demo/src/main.tsx байтово одинаковы на момент чтения. HashRouter; routes:
`/`, `/orders`, `/orders/:id`, `/products`, `/inventory`, `/pricing`, `/integrations`, `/integrations/add`, `/automation`, `/settings`, wildcard.

initialAccounts создаёт 3 WB + 5 Ozon + 1 Yandex + 1 МойСклад; seedOrders — 36 синтетических заказов; chartData — статические ряды. Нет fetch и localStorage в main.tsx; действия изменяют useState, показывают toast/modals, работают лишь в памяти. Это MOCK даже если кнопка активна, меняется status или рассчитывается итог.

Recharts используется для графиков; motion для анимации с reduced-motion настройками; lucide-react для иконок. Hero3D lazy-loads react-three/fiber Canvas, Three.js/drei, procedural geometry и frame rotation. В просмотренных src/public не найдено GLB/GLTF asset loading; импортируемые логотипы — изображения. Нельзя утверждать наличие сложной 3D asset pipeline.

## Лендинг

E:/work/styk-landing/src/main.tsx — marketing React page без react-router. Anchors features/integrations/how/pricing, demo CTAs указывают /demo/. Dashboard/Chart — иллюстрации с demo labels. SupportWidget содержит заранее заданные вопросы, не подтверждённый helpdesk backend. ConnectionScene.tsx — responsive SVG paths + animateMotion + IntersectionObserver, не WebGL.

Build chain: nested demo build → TS/Vite landing → prerender.mjs → copy-demo.mjs. Legal offer/privacy/consent находятся в public/legal, явно draft/non-effective и содержат placeholders. Google fonts и map iframe создают внешние загрузки; privacy implications требуют review, здесь requests не выполнялись.

## Расхождение обещаний

| Демонстрация | Рабочий кабинет |
|---|---|
| Все WB/Ozon/Yandex shops | MS/Ozon adapters only |
| Выручка, прибыль, сравнения | Нет financial backend |
| Заказы со сменой статуса | Read-only order evidence, нет fulfillment |
| Массовые цены/наценки | Ограниченные catalog capabilities, нет pricing module |
| Автоматизации и профиль/тариф | Специализированный тестовый sync workflow, billing нет |
| Несколько магазинов в памяти | Persistent scoped connections, требует реального lifecycle и E2E |

Демо годится как UX-прототип. Использовать его как спецификацию реализованного SaaS нельзя. Дизайн и копирайт требуют явной release capability matrix. Подробности фирменного стиля — в 16.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [web/src/App.tsx:1](../../web/src/App.tsx#L1) | 73: State; 80: Notice; 83: Loading; 86: Failure; 93: scopeParams; 101: SourceCard; 169: Overview; 235: Products; 245: StockRows; 315: WarehouseList; 449: Warehouses; 525: checkSummary |
| [web/src/main.tsx:1](../../web/src/main.tsx#L1) | configuration / declarations; see file |
| [web/src/api.ts:1](../../web/src/api.ts#L1) | 4: setCSRF; 179: createImportIntent; 287: createConfirmationIntent |
| [web/src/AppShell.tsx:1](../../web/src/AppShell.tsx#L1) | 14: Navigation; 31: AppShell |
| [web/src/MerchantAuth.tsx:1](../../web/src/MerchantAuth.tsx#L1) | 6: fragment; 16: hasAuthLink; 20: MerchantAuth; 260: requestFor |
| [web/src/CatalogSync.tsx:1](../../web/src/CatalogSync.tsx#L1) | 36: CatalogSync; 52: CatalogSyncAccount; 354: manualValues; 368: enrollmentJSON |
| [web/src/Integrations.tsx:1](../../web/src/Integrations.tsx#L1) | 42: accountLabel; 80: Integrations |
| [web/vite.config.ts:1](../../web/vite.config.ts#L1) | configuration / declarations; see file |
| [E:/work/styk-demo/src/main.tsx:1](E:/work/styk-demo/src/main.tsx#L1) | 17: Brand; 18: Logo; 19: Status; 20: Card; 21: Button; 23: App; 28: PageHeader; 29: Empty; 30: Dashboard; 31: OrderTable; 32: Orders; 33: OrderDetails |
| [E:/work/styk-demo/src/Hero3D.tsx:1](E:/work/styk-demo/src/Hero3D.tsx#L1) | 5: Objects |
| [E:/work/styk-landing/src/main.tsx:1](E:/work/styk-landing/src/main.tsx#L1) | 9: Icon; 11: BrandImage; 12: Logo; 13: DemoButton; 14: Badge; 15: Chart; 16: Dashboard; 17: SupportWidget; 49: ConnectionBenefits; 54: App |
| [E:/work/styk-landing/src/ConnectionScene.tsx:1](E:/work/styk-landing/src/ConnectionScene.tsx#L1) | 18: Paths; 27: ConnectionScene |
