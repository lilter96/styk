# Неизвестное и вопросы следующему архитектору

## P0: до контрактных обещаний и production

1. Каков первый продаваемый scope: read-only visibility/mapping, assisted pilot или реальная автоматическая publication? Кто утверждает ограничения?
2. Какой provider contract даёт достаточное доказательство целостности reserve/order coverage и write fencing? Какие uncertainty допускаются бизнесом?
3. Почему падают семь stock history/replay tests? Известен лишь факт прежнего прогона и воспроизведения одного на HEAD; root cause нельзя угадывать.
4. Какая release policy исключает integration fixtures/diagnostic tools из production artifacts?
5. Где будет trusted proxy, как сохраняет Host/Origin и forwarded peer, где TLS termination и API loopback?
6. Каковы secret manager, key access separation, key rotation и DB+key restore procedures?
7. Кто владелец backups/incident response, какие RPO/RTO/SLO, кто получает unresolved outcomes alerts?
8. Какое юридическое лицо заключает договор и является data controller/processor; утверждены ли реквизиты/юрисдикции?
9. Как будут приниматься деньги, продлеваться и отменяться подписки; какая currency/tax/refund модель?
10. Какие условия фактического production rollout разрешены владельцем отдельно от fixed test-account authority?

## Продуктовая модель

- Должен ли один tenant поддерживать нескольких пользователей? Текущий saas_merchant_accounts связывает user и tenant one-to-one, OWNER-only.
- Можно ли один external account использовать в нескольких tenant (агентства)? Сейчас UNIQUE(source,account_id) препятствует этому.
- Нужны ли несколько МойСклад accounts, или бизнес ограничит одним? Scope model допускает pairs, UX/операционные правила не утверждены.
- Цена 5 000 RUB — target hypothesis или утверждённый тариф? Сколько accounts/SKU/задач включает?
- Какие WB/Yandex workflows обязательны MVP, какие могут быть явно coming later?
- Какие analytics definitions нужны: gross revenue/net payouts/profit/fees/COGS, timezone/currency/returns?
- Каков source of truth для каждого catalog/price/stock field? Кто решает конфликт?
- Поддерживаются ли комплекты/упаковки/единицы/общие склады/shared item targets и по каким allocation rules?
- Какие external writes создают реальные обязательства и кто вправе их подтвердить?

## Внешние контракты и эксплуатация

- Актуальны ли Ozon methods/schema/retirement dates для целевых кабинетов сегодня?
- Какие права токена доступны/необходимы; как определять revoked/expired/insufficient scope?
- Общие provider rate limits считаются на key/account/IP/организацию? Как делятся между людьми и workers?
- Какова максимальная разрешённая задержка stock freshness, import completion и order coverage?
- Как reconcile provider task с неоднозначным status или частично заполненным receipt?
- Где limits по storage/retention/response bytes позволяют отказ, а где требуется sharding/checkpoint?
- Как масштабировать worker без starvation и нарушения account quota?
- Нужен ли generic broker/cache? Ответ — после профиля, не предположение.
- Существуют ли вне этого repo IaC, support platform, observability, production deployments? В доступном source не подтверждены.

## Право и данные

Список профессиональной проверки в 11. Неизвестны locations/subprocessors, processing purposes, retention, deletion/export procedures, cookie/third-party policies, acceptance evidence, corporate/IP agreements. Эти ответы нельзя вывести из draft legal HTML.

## Что проверить отдельным разрешённым этапом

Чистый disposable checkout с full verification; dedicated DB schemas/migrations/constraints; source-to-UI E2E; lock-wait expiry; unknown/partial outcome recovery; permission-revocation; dependency SCA; queue/DB load; restore/decrypt. В этом Read-Only аудите такие команды не запускались, чтобы не менять состояние или создавать данные.

## Как обновлять аудит

Зафиксировать commit + dirty snapshot provenance, повторить exact route/schema inventory, приложить command outputs без secrets и указать, какие UNKNOWN превратились в CONFIRMED. Не менять статусы на IMPLEMENTED только по PR title, макету или документации.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/config/stock_publication.go:1](../../internal/config/stock_publication.go#L1) | 13: StockPublicationTestEndpoint |
| [internal/identity/merchant.go:1](../../internal/identity/merchant.go#L1) | 13: MerchantTenant |
| [migrations/00039_merchant_accounts.sql:1](../../migrations/00039_merchant_accounts.sql#L1) | 6: saas_merchant_accounts |
| [Dockerfile:1](../../Dockerfile#L1) | configuration / declarations; see file |
| [compose.yaml:1](../../compose.yaml#L1) | configuration / declarations; see file |
