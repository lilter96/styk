# Независимые LIVE-readback

## Записи МойСклад

В [operations.jsonl](operations.jsonl) **ровно 5 строк — 5 фактических записей**.
Все получили HTTP 200 и затем отдельный GET, все RECONCILED:

1. Создан выделенный нейтральный статус A (`Regular`), GET точного ID подтвердил account/name/type.
2. Создан выделенный нейтральный статус B (`Regular`), такой же независимый GET.
3. Создан явно помеченный `STYK-LIVE-20261107-*` пустой customerorder, `applicable=false`, состояние A. GET подтвердил account, marker, состояние, непроведённость; GET positions вернул пустой массив.
4. Только у этого тестового заказа выполнен A→B. Новый GET подтвердил B и прежний marker/account; позиции остались пустыми.
5. Выполнен B→A. Новый GET подтвердил A и `applicable=false`; позиции остались пустыми.

Masked customerorder: `0c4dc0402313`. Тестовые статусы и заказ оставлены с явной
пометкой LIVE для аудита; stock/financial compensation не требуется. Ни товаров,
ни резерва, ни реального покупательского отправления у этого актива нет.
Бизнес-заказы, документы бухгалтеров и их статусы не изменялись.

Штатный `ReadNativeOrderChanges` с documented updated-фильтром нашёл настоящий
ID после каждого изменения. Повторный опрос также нашёл его. Затем **новый
процесс** с заново разрешёнными encrypted credentials прочитал тот же marker,
account, A, `applicable=false`, пустые позиции и updated-поток. Дополнительных
POST/PUT при этом не было.

Это LIVE-доказательство нативных статусов и polling-контракта. У тестового
заказа намеренно нет выдуманной Ozon-корреляции: merchant-intent → `/ship` и
полный reverse lifecycle этим не подтверждены.

## Штатный application ingestor

На существующей включённой паре запущен настоящий `Ingestor.CollectStream`,
штатный Factory, encrypted Resolver, PostgreSQL leases и provider coordinator.
Повторный single-process updated-проход завершился успешно. Затем два
конкурентных экземпляра выполнили тот же поток: оба завершились без ошибки,
provider сделал только одну пару GET (identity + страница).

До/после: **8 work / 8 work, 0 merchant intents / 0 intents, 0 внешних записей**.
Неизвестная тестовая корреляция не присвоена, новые outbox-команды не созданы.
Существующие lifecycle/finance grants не включались. Первый проход native-points
вернул null error, но последующий updated GET завершился transport failure;
этот остановленный/неполный проход не назван PASS целиком. Повторён только
безопасный read, внешние UNKNOWN-записи не повторялись.

## Ozon и финансы

Два Express-отправления с `tpl_provider_id=-2` после исправления заново прочитаны
с production Ozon API. Обе карточки cancelled, normal parser больше не падает,
полномочий на доставку из отрицательного ID не появилось.

Все семь найденных финансовых источников заново прочитаны после component fix.
Отчёт за октябрь отдельно прошёл настоящий `Service.RealizationReport` с текущими
scope/config/credential guards: 5 строк, стабильный source fingerprint. Ни один
отчёт не объявлен финансовым документом МойСклад. NULL возврата не превращён в
ноль; поддерживаемая финансовая запись не обнаружена и не подменена тестовой.

Приватные обезличенные журналы: `.local/live-orders-2026-11-07/`:
`inventory.jsonl`, `provider-fix-readback.jsonl`, `finance-live-readback.jsonl`,
`finance-service-live.jsonl`, `native-inventory.jsonl`, `native-canary.jsonl`,
`canary-readback.jsonl`, `native-ingestor-*.jsonl`. Raw buyer JSON и токены
в публичные артефакты не входят. Пересекающиеся окна provider-permit журналов
нельзя суммировать: при подсчёте используются уникальные permit IDs.


## Source → PostgreSQL и отдельное native coverage

Настоящий Source Ingestor/Factory/provider coordinator выполнил discovery,
unfulfilled и active cycle на обеих существующих парах; source pages и
checkpoints сохранены в shared dev. Discovery/unfulfilled на обеих парах
завершили свои текущие окна. Первый active batch второй пары оставил cursor
(`complete=false`, `exhausted=false`): это одна штатная ограниченная страница,
не заявление о завершённом обновлении всего background backlog. Годовая
инвентаризация всех 380 отправлений выше выполнена отдельно до последнего
cursor. В этом source cycle work остались 8, intents 0, внешних записей 0.
Артефакт: `source-ingestor-live.jsonl`.

[Дополнительный исправленный native read](demand-coverage.md) подтвердил
отсутствие связанных отгрузок у всех трёх sparse parents. Два одинаковых
полных account-wide списка и права ALL не заменены `demands=[]` в header.
50 уникальных дополнительных GET завершились HTTP200; физические записи не
выполнялись. Оставшаяся гонка с внешним автором после GET описана отдельно.
