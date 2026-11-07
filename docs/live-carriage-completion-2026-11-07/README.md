# Доработка Ozon Carriage — 2026-11-07

Сняты безусловные программные блокировки CREATE/APPROVE. Реализованный путь — обычный FBS, non-Express, dropoff Ozon, непустой полностью проверенный состав. **Новая успешная запись CREATE/APPROVE на production пока не подтверждена: в доступных аккаунтах нет готовой партии.** Полноценный HTTP/PostgreSQL/outbox/worker цикл проверяется синтетическими провайдерами отдельно от LIVE.

## Причины прежней блокировки и исправления

1. Decoder `/v2/carriage/delivery/list` понимал только пустой `methods`; любая непустая выдача блокировалась. Теперь читаются точные method/warehouse/date, `mandatory_packaged_count + optional_packaged_count`, открытые carriage IDs, critical ошибки Ozon. Формат подтверждён authenticated непустым ответом на историческую дату.
2. Каждый known-ID read добавлял `CARRIAGE_APPROVAL_CONTRACT_UNVERIFIED`; APPROVE outcome всегда был false. Теперь `new` допускает отдельное подтверждение, а успех требует независимого readback документов и состава.
3. Обычный справочник `/v2/delivery-method/list` описывает rFBS и может не содержать FBS method. Для carriage точная method identity берётся из discovery/header и связывается со свежими явными warehouse facts FBS/non-rFBS/non-Express. Отсутствующее поле не считается false. Противоречия и ошибки значимых полей posting блокируют запись; не относящиеся к carriage финансовые поля её не блокируют.
4. Промежуточная страница unfulfilled имеет `complete=false` до исчерпания cursor. Теперь собираются все страницы; любая ошибка detail GET, повтор cursor/identity или несовпадение packaged count останавливает запись. Время окна ограничено 31 днём; совпадение счётчиков обязательно, выход за окно не выдаётся за полный состав.
5. Post-write сравнение включало изменяемые status/readiness/source fingerprint. Теперь outcome сравнивает exact posting/scheme/method/warehouse/SKU/offer/quantity; перед отправкой по-прежнему проверяется полный свежий fingerprint. Несовместимые статусы участника carriage и posting не дают согласовать APPROVE.
6. После подтверждённого APPROVE новая команда CREATE могла навсегда упираться в прежний global head. Fence освобождает завершённый APPROVE только для той же scope и нового полного preview без открытых кандидатов. UNKNOWN, чужой scope и незавершённая операция остаются fenced.
7. `/v2/posting/fbs/act/check-status` классифицирован как POST-чтение. В UI отдельно объясняются отсутствие готовой партии, `outdated`, незакрытая партия и неподходящий профиль.

## Первичные источники и контракт результата

- [Официальный Seller OpenAPI](https://docs.ozon.ru/api/seller/zh/swagger.json): CREATE принимает delivery_method_id/int64, all_blr_traceable, optional departure_date; возвращает carriage_id. APPROVE принимает carriage_id и условный containers_count; HTTP200 может быть без тела. В GET status задан строкой без enum.
- [Официальная миграция на create/approve](https://dev.ozon.ru/news/789-Izmeneniia-v-metodakh-otgruzki-Seller-API/).
- [Переход на v2 carriage discovery](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/).
- [Официальные правила состава партии](https://dev.ozon.ru/news/486-Izmeneniia-v-protsesse-otgruzki-tovarov-v-Seller-API/): включаются готовые отправления; отменённые после создания не удаляются автоматически.
- [Официальный канал Seller API, обновление act/check-status](https://t.me/s/OzonEnSellerAPI?before=313).

Main Swagger недоступен целиком через текущие средства чтения; полнота его выгрузки не заявляется. Точные runtime shapes получены от authenticated production API. Зашифрованные raw responses находятся локально; публичный `operations.jsonl` содержит только маски, endpoint, статус, revision, request digest и ciphertext digest. Customer data, адреса, секреты и raw provider IDs в журнал не включены.

APPROVE reconciliation — проверка наблюдаемого эффекта, не догадка о строковом enum: прежний header `new`; новый header с тем же account/id/method/warehouse, продвинувшимся updated_at и статусом вне `new`/`cancelled`; один из опубликованных `get_shipping_list` / `get_act_of_acceptance` / `get_waybill`; независимый `check-status` с `status=ready`, `is_partial=false`, без removed и с exact added posting IDs. Если передан containers_count, GET должен подтвердить его точно. Header и membership повторно читаются вокруг posting/doc reads. ACK не равен reconciliation.

## LIVE-наблюдения

Проверены 2 реальных Ozon аккаунта и identity 2 подключённых МойСклад аккаунтов. Исследованы 2 различных реальных отправления: участник исторической партии первого аккаунта и текущее `delivering` второго. Готовых `awaiting_deliver` не обнаружено.

| Проверка | Первый Ozon (маска b13240589abe) | Второй Ozon (маска cc964f192932) |
| --- | --- | --- |
| Текущий v2 carriage discovery | 200, methods пуст | 200, methods пуст |
| Act history за год | 1 закрытая партия, 2026-10-14 | 0 партий |
| Discovery по её реальным method/date | 1 method, closed carriage, packaged=0, critical `outdated` | Пустой methods |
| Known-ID read через обычный Service/Factory/gate | Полный header/members/header, 1 участник, documents `ready` | 404; чужая identity не принята |
| Текущий unfulfilled, 31-дневное окно | 0, has_next=false | 1 `delivering`, has_next=false; detail GET подтверждён |
| Новые CREATE/APPROVE | Не выполнялись: нет готовой партии | Не выполнялись: нет готовой партии |

Количество вызовов и результатов фиксируется в `verification.md` по окончательному журналу. **Provider WRITE = 0** в этой доработке. Исторический `ready` не выдаётся за исполненный новый APPROVE. Отправления не собирались, не переводились в ship/handoff и не подменялись синтетическими реальными заказами. UNKNOWN-операции не повторялись. В shared dev сохранены только штатные read previews/admission/session audit; новая миграция и включение фонового worker не требовались. Push/merge/deployment не выполнялись.

## Что ещё нужно подтвердить на настоящем batch

Нужна фактически собранная FBS/dropoff партия с awaiting_deliver, verified non-Express профилем и актуальной датой. Тогда CREATE выполняется один раз с согласованным полным составом, ID сохраняется, header/members проверяются; APPROVE требует нового preview и отдельного согласия, после чего проверяются документы. В этой доработке такой batch объективно отсутствовал; live mutation/receipt/formation остаются NOT_OBSERVED.

Нет provider idempotency token, posting allowlist или compare-and-swap для CREATE. Другой сотрудник может изменить состав после последнего GET; после POST любое расхождение блокирует продолжение, STYK не переписывает состав чужой партии. Потерянный CREATE ACK без ID требует расследования identity; автоматического повтора/усыновления по дате нет. После неизвестного APPROVE допустимы чтения, без повторного POST.

ETTN/`all_blr_traceable=true`, rFBS, Express, pickup и сторонние carrier integrations в этом пути не поддержаны. Containers count требует реального режима доверительной приёмки. Документы партии не доказывают физическую передачу, native Отгрузку МойСклад, доставку покупателю или выплату. Для первых продавцов остаётся обязательной наблюдаемая LIVE-проверка нового batch и корректная эксплуатационная конфигурация workers; отсутствие этого evidence нельзя объявлять полным production acceptance.
