# LIVE-матрица сценариев

`LIVE_PASS` означает наблюдённое production API поведение указанного среза,
не всего заказа. Автоматические тесты не заменяют LIVE evidence.

| Сценарий | Результат | Факты / граница |
| --- | --- | --- |
| Все четыре реальные подключения | LIVE_PASS | 2 Ozon + 2 МойСклад; ownership/permissions проверены по scope |
| Ozon discovery/detail и unfulfilled | LIVE_PASS | 380 карточек, два account, явные окна и исчерпанные cursor pages |
| Исправленное чтение Express provider -2 | LIVE_PASS | Два fresh GET; неизвестный перевозчик не получил права на действия |
| Нативные metadata/связанные заказы/существующая draft demand | LIVE_PASS | Две пары, четыре customerorder, одна существующая draft demand |
| Нативные нейтральные состояния | LIVE_PASS | 5 записей, 5 отдельных readback, пустой непроведённый тестовый актив |
| Updated polling и повторный read после restart | LIVE_PASS | Реальный test ID найден; неизвестная корреляция не присвоена |
| Конкурентный leased updated ingestor | LIVE_PASS | Два экземпляра, одна пара GET, work/intents не размножились |
| Отдельное доказательство demand absence при missing/NULL | LIVE_PASS | Три реальных parent, два полных списка по 3 документа, ALL до/после, прежний raw revision; 0 записей |
| Stock/reserve read | LIVE_PASS | Штатный отчёт: 10/3/7 и явные 0/0/0; physical write не было |
| FBS pack через merchant transition и `/ship` | NOT_OBSERVED_LIVE | Нет подходящего ready posting / подтверждения упаковки |
| rFBS non-integrated delivering/last-mile/delivered | NOT_OBSERVED_LIVE | Не найден новый проверенный собственный rFBS физический маршрут |
| Integrated/aggregator seller delivery command | NOT_APPLICABLE | Доставка принадлежит перевозчику; полномочия не выдуманы |
| Реальный handoff → applied demand → reserve release | NOT_OBSERVED_LIVE | Нет нового физического handoff; существующая demand — draft |
| Реальный возврат → applied salesreturn | NOT_OBSERVED_LIVE | Нет подтверждённого физического возврата и подходящей applied basis |
| Отсутствие физического события не создаёт документы | LIVE_PASS | Нулевые новые demand/salesreturn/Ozon fulfillment writes |
| Monthly source read / новый PUP component | LIVE_PASS | 7 production reports, 254 строки, точные суммы/NULL/zero сохранены |
| Native commission draft по реальному источнику | LIVE_BLOCKED | Нет Commission/None договора; реальные источники не проходят текущие gates |
| Source correction → ручной diff | CONTRACT_ONLY_PASS | Реальное исправление поставщиком в этом запуске не наблюдалось |
| UNKNOWN external write → read-only reconciliation | CONTRACT_ONLY_PASS | Ни одного LIVE UNKNOWN в этом запуске; искусственный lost ACK не создавался |
| Carriage create/approve | NOT_OBSERVED_LIVE | Нет готового проверенного batch; writer отсутствует, discovery contract не подтверждён |
| Полный fulfillment первого продавца | NOT_OBSERVED_LIVE | Требуются реальные физические события и завершение оставшихся gates |

## Eligibility конкретных данных

| Набор | Eligibility | Причина |
| --- | --- | --- |
| Все 380 отправлений | READONLY_ELIGIBLE | Реальные scope и свежие карточки |
| 379 terminal: 293 delivered + 86 cancelled | NOT_ELIGIBLE для нового fulfillment | Исторический финал не является новым pack/handoff/return |
| `e63fcc1352bc`, delivering / posting_in_pickup_point | READONLY_ELIGIBLE | Не ready; нет физического подтверждения и advertised seller actions |
| FBS_PACK_ELIGIBLE | Не найдено | Нет awaiting_packaging с разрешёнными prerequisites и фактической готовностью |
| RFBS_NONINTEGRATED_ELIGIBLE | Не найдено | Не найден новый проверенный маршрут с реальным событием |
| HANDOFF_ELIGIBLE / RETURN_ELIGIBLE | Не найдено | Физические события не наблюдались |
| FINANCE_DRAFT_ELIGIBLE | Не найдено | Семь источников требуют бухгалтерской/компонентной проверки |
| UNKNOWN_PENDING | Не обнаружено | Начальный ledger не содержал UNKNOWN; все 5 записей RECONCILED |
