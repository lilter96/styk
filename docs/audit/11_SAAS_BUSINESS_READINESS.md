# Коммерческая, юридическая и операционная готовность

## Реальный user journey

| Этап | Что видит клиент | Реальная возможность/разрыв |
|---|---|---|
| Landing | три маркетплейса, заказы, цены, аналитика | visual positioning, не backend capability contract |
| Demo | shops, orders/statuses, pricing, charts | synthetic arrays/useState, без API |
| Переход к сервису | demo CTA на /demo/ | production signup conversion route/deployment не подтверждены |
| Регистрация | MerchantAuth email flow | код реализован; нужен корректный SMTP и delivery monitoring |
| Верификация/пароль | email fragment link | код есть, юридическое acceptance не встроено |
| Подключение | MS/Ozon credential editor | server probe/encryption/scoped save; WB/Yandex отсутствуют |
| Первый импорт | durable job/progress | зависит от credentials/worker/limits, это чтение |
| Сопоставление | products/warehouses/review confirm | требует human identity review, не автоматическое доказательство |
| Первая синхронизация | catalog/stock controls | для обычного клиента write gates закрыты |
| Ежедневная работа | observations/doctor/history | нет общего fulfillment/order cabinet/analytics |
| Оплата/подписка | marketing price concept | billing, entitlements, checkout отсутствуют |
| Отмена/экспорт/удаление | готового journey нет | требует продуктовой и правовой реализации |

## Пригодность к продажам

Самостоятельная регистрация и чтение source data имеют реализацию; paid end-to-end journey отсутствует. Основной blocker — клиент покупает ожидание автоматического cross-marketplace управления, а код предоставляет ограниченный MS/Ozon engineering workflow.

Нет production подтверждения: что пользователь сам подключит accounts с нужными scopes, синхронизирует произвольный каталог/остатки, оплатит и отменит подписку, восстановит failed job без инженера. Поэтому продавать как «готовую автоматизацию трёх маркетплейсов» сейчас нельзя. Узкий assisted pilot должен иметь отдельное описание ограничений и обязательств.

## Юридические артефакты

Соседний landing/public/legal содержит offer.html, privacy.html, consent.html. Все три явно draft/non-effective и содержат незаполненные реквизиты. Их наличие не означает действующую оферту или согласие.

Backend API/schema не показывают:
- versioned acceptance records (document/version/user/time/evidence);
- billing agreement/renewal/cancellation/refund implementation;
- account deletion, data subject export, retention execution;
- confirmed legal entity/registration details;
- consent preferences и отзыв маркетингового согласия.

Immutable security/domain audit уже есть, но это не ledger принятия оферты. Юридические drafts местами говорят об отсутствии регистрации; current merchant code уже реализует её. Нужна синхронизация юридического описания с фактической processing map.

## Вопросы для юристов BY/RU

Это список для профессиональной проверки, не заключение о применимом праве:
1. Кто contract party и data controller/processor при белорусском ООО и клиентах РФ/РБ?
2. Какие данные реально собираются для регистрации, provider accounts, order identifiers, логов и поддержки?
3. Где будут hosting/backup/SMTP/subprocessors, какие передачи между странами возникают?
4. Какие требования к localization, уведомлениям и договорному поручению обработки применимы к выбранной модели?
5. Как оформить recurring subscription, currency/tax/invoice/refund/cancellation и отсутствие гарантии vendor API availability?
6. Как разделить authorization клиента на marketplace access и согласия субъектов данных?
7. Какие сроки хранения допустимы для immutable audit и как реализовать deletion/export без разрушения обязательной истории?
8. Какую acceptance evidence хранить, как version documents и фиксировать изменение условий?
9. Есть ли права на бренды/logos/шрифты и корректные disclosures external fonts/maps?
10. Как юридически оформить двух участников 50/50, IP ownership, deadlock, выход/отчуждение долей и ответственность? Код не отвечает на корпоративные вопросы.

## Поддержка и эксплуатация

Landing SupportWidget — предзаданные FAQ, не подтверждённый ticketing/SLA. В приложении есть diagnostics и operation states, но нет подтверждённого customer support workflow, on-call, incident communications, публичной status page или service credits policy.

Неизвестны RPO/RTO, uptime objectives, data retention, cancellation grace period, quota policy и единицы тарификации (tenant/account/SKU/job). Их нельзя подменять техническими import limits.

## Критерии первого платного клиента

Утверждённый narrow feature contract; authenticated onboarding со scope checks; production-safe sync вертикальный срез; measurable support/recovery; legal documents/acceptance; работающий billing+entitlements; резервное восстановление; воспроизводимо зелёный release gate. Пока эти критерии не выполнены, статус — pre-commercial prototype, не произвольный процент готовности.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/console/merchant_routes.go:1](../../internal/console/merchant_routes.go#L1) | 5: registerMerchantRoutes |
| [internal/identity/merchant.go:1](../../internal/identity/merchant.go#L1) | 13: MerchantTenant |
| [web/src/App.tsx:1](../../web/src/App.tsx#L1) | 73: State; 80: Notice; 83: Loading; 86: Failure; 93: scopeParams; 101: SourceCard; 169: Overview; 235: Products; 245: StockRows; 315: WarehouseList; 449: Warehouses; 525: checkSummary |
| [E:/work/styk-landing/src/main.tsx:1](E:/work/styk-landing/src/main.tsx#L1) | 9: Icon; 11: BrandImage; 12: Logo; 13: DemoButton; 14: Badge; 15: Chart; 16: Dashboard; 17: SupportWidget; 49: ConnectionBenefits; 54: App |
| [E:/work/styk-landing/public/legal/offer.html:1](E:/work/styk-landing/public/legal/offer.html#L1) | configuration / declarations; see file |
| [E:/work/styk-landing/public/legal/privacy.html:1](E:/work/styk-landing/public/legal/privacy.html#L1) | configuration / declarations; see file |
| [E:/work/styk-landing/public/legal/consent.html:1](E:/work/styk-landing/public/legal/consent.html#L1) | configuration / declarations; see file |
