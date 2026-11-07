# LIVE-приёмка STYK — 2026-11-07

Выполнены реальные production API-вызовы через существующие encrypted подключения
shared dev-БД. Это не sandbox и не отчёт только по mocks. Push, merge и
production deployment не выполнялись. Shared dev после backup обновлён 71→73.

## Точные результаты

- **4 аккаунта:** 2 Ozon + 2 МойСклад, 2 workspace, 2 настроенные пары.
- **380 реальных отправлений** в заявленном годовом окне: 293 delivered,
  86 cancelled, 1 delivering / posting_in_pickup_point. Готовых к сборке нет.
- **5 реальных API-записей**, все в основном МойСклад, все HTTP200 +
  независимый GET/readback → RECONCILED: 2 нейтральных статуса, 1 пустой
  непроведённый test customerorder, 2 изменения A→B→A.
- **0 Ozon fulfillment/stock writes, 0 applied demand, 0 salesreturn,
  0 commissionreportin, 0 платежей, 0 UNKNOWN write/retry.**
- Четыре настоящих связанных native customerorder, одна существующая draft
  demand и точные stock/reserve pools прочитаны без изменения.
- **7 реальных месячных финансовых источников / 254 строки** прочитаны после
  parser fix. Всего проверено 24 account-period, 17 ответили 404.
- Штатные encrypted Resolver/Factory/Service, updated polling, PostgreSQL lease
  и два конкурентных ingestor экземпляра использованы реально. Повторный
  процесс подтвердил тестовый заказ без новых записей. Outbox/work дубли не
  появились. Полный merchant transition→Ozon fulfillment этим не подтверждён.

## Автоматизированная проверка

Итоговый **make check-full: PASS за 259.329 с (4:19.329)**, включая PostgreSQL,
сборки и cleanup. Go race: 1628 top-level PASS / 27 SKIP / 0 FAIL; business
582 имени учтены (566 PASS / 16 SKIP); frontend 403 теста и 60 browser scenarios
PASS. Исходники во время проверки не менялись. Подробные skips и дополнительные
неуспешные проверки раскрыты в [verification.md](verification.md).

## Исправлено

1. Отрицательный carrier/provider ID Ozon `-2` больше не теряет историческое
   Express-отправление; строгие остальные IDs и запрет неподтверждённых действий
   сохранены. Повторный production read двух карточек успешен.
2. Реальный новый `pick_up_point_coinvestment` сохраняется точно, участвует в
   fingerprint и проверяется до native draft dispatch. Старые hashes/UNKNOWN
   readback сохранены; NULL/missing не превращены в ноль.

3. Для missing/NULL `customerorder.demands` добавлено отдельное полное
   account-wide доказательство отсутствия отгрузок: ALL до/после, два
   одинаковых обхода, неизменённый полный parent, повторный preflight.
   Три реальных parent прошли read-проверку; raw provider JSON не переписан.
   Действительная applied demand этим не подтверждена.

[Детали покрытия native demand](demand-coverage.md). Финальная автоматизированная
проверка и её точное время — в [verification.md](verification.md).

## Что объективно не подтверждено

Нет нового реального физически подготовленного FBS posting, собственного
неинтегрированного rFBS маршрута с новым событием, подтверждённого handoff,
физического возврата или applied basis для него. Статусы `delivering`,
`delivered`, сборка и сформированная carriage не подменяют эти события.
Искусственные `/ship`, отмена покупателю, доставка/возврат не выполнялись.

Реальные финансовые источники не проходят нынешнее sales-only подмножество
(в том числе NULL/ненулевые returns и неизвестные компоненты); подходящего
комиссионного договора/согласованной policy нет. Поэтому native draft не назван
LIVE_PASS. Source correction и потерянный ACK проверены контрактными тестами;
реальных таких событий в этом запуске не было.

## Что препятствует первым продавцам

Для ограниченного запуска нужен продавец с настоящим безопасным ready order,
подтверждёнными product/warehouse mappings и актуальным carrier profile. Владелец
должен привязать состояния к действительным операциям склада и отдельно выдать
scope/revision consent; текущие подключения таких lifecycle/finance grants не
имеют. Первый pack, реальный handoff, native reserve release и настоящий return
нужно завершить с readback на этом продавце. До этого нельзя заявлять production
готовность полного fulfillment.

Carriage batch writer не реализован; current discovery/approval evidence остаётся
contract verification gap, а не запрет API. Для финансов нужны настоящий
Commission/None договор, явные price/rate/VAT решения бухгалтера и поддержка
реально встреченных return/service/component форм. Ошибки/UNKNOWN и внешние
ручные документы требуют сверки; межсервисной транзакции и verified conditional
native-create CAS нет.

## Захват следующего подходящего заказа

Существующее source чтение разрешено на обеих парах, штатный интервал — 300 с.
После согласованного локального запуска обновлённых API/worker одной версии
первым проверяется fresh discovery/unfulfilled/detail и текущая ingestion lease.
Обычный worker в этой LIVE-приёмке постоянно не оставлен: он способен обрабатывать
другие включённые account jobs. Новые тестовые grants/автозаписи не включены.

Для следующего реального `awaiting_packaging`: отдельно подтвердить владельца,
схему/перевозчика/Express, exact composition, marking/country/exemplars и
фактическую готовность склада; установить baseline той же config/credential
revision, выполнить реальное изменение merchant state и проследить **один**
intent→work→provider attempt→independent readback. При UNKNOWN — только точные
чтения исходной операции, без повторного POST. Для handoff/return нужна новая
самостоятельная физическая декларация. Это конкретный следующий LIVE gate,
не пропущенный зелёный тест.

Подробности: [инвентаризация](inventory.md), [операции](operations.jsonl),
[матрица](scenario-matrix.md), [readback](readback.md), [дефекты](failures.md),
[backup и runtime](runtime-migration.md), [demand coverage](demand-coverage.md).
