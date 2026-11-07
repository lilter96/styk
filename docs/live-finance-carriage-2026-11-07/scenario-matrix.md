# Матрица сценариев

| Сценарий | LIVE результат | Граница доказательства |
|---|---|---|
| Normal Resolver + точные credential revisions | Подтверждено на четырёх подключениях | Не доказывает права на все возможные записи |
| Месячная реализация | 7 отчётов/254 строки повторно прочитаны; normal Service классифицировал и сохранил ledger | 17 provider 404 сохранены как отсутствие доступного source, не как нулевой отчёт |
| Полная классификация source | 254/254 строки; 252 SALE, 1 MIXED, 1 RETURN | Классификация не равна native mapping или учёту выплаты |
| Exact component identity | 255/255 ненулевых sale/return групп: amount−standard_fee+bonus+bank_coinvestment=total | Это наблюдаемое равенство источника, не универсальная бухгалтерская политика |
| Native projection discrepancy | 231 группы имеют bank residual; старое равенство без bank совпало только для 24 | Bank не подмешан в комиссию или цену |
| Policy-off preview | 14 заблокированных preview для 7 отчётов сохранены; 14 попыток approval отвергнуты | Нет разрешения создавать финансовый документ без политики |
| Native return positions | Реальный MS API контракт реализован; fixture HTTP/PG regression | Реальный финансовый return draft не создан: BLOCKED_ACCOUNTING_POLICY |
| Финансовый draft/applicable=false | BLOCKED_ACCOUNTING_POLICY | Нет подходящего подтверждённого договора/политики; real POST не выполнялся |
| Отдельные accrual types | 138 типов на каждом Ozon | Отдельный source family, не прибавляется к realization |
| Accrual postings | Все 380 реальных номеров: 9 + 200 + 171; typed complete, unknown 0 | Начисление не является доказательством перечисления денег |
| Accrual by-day 2026-10-01 | 0 записей на первом, 1 item-fee на втором | Дополнительное quantity сохранено как unknown/review, не отброшено |
| Carriage discovery | Обе реальные выдачи methods=[], cursor="", has_next=false | Непустой schema/membership не доказан пустым ответом |
| Carriage negative preview | Normal Service: NO_AVAILABLE_BATCH + READY_MEMBERS_REQUIRED для обеих пар | Никакого enqueue или внешней записи |
| Историческая carriage | Normal Service прочитал реальный ID, closed, 1 member; approval preview заблокирован; тот же ID в чужом аккаунте — 404 | closed не превращён в выдуманное доказательство нового approve |
| Carriage CREATE / APPROVE | NOT_OBSERVED_LIVE | Нет готовой партии; дополнительно отсутствует подтверждённый nonempty/approval-state контракт |
| Физическая передача / складской документ / выплата | NOT_OBSERVED_LIVE в этом запуске | Эти события нельзя выводить из formation, monthly report или accrual |
| UNKNOWN dispatch | Реальных UNKNOWN нет; fixture lost-ACK guards тестируются отдельно | Повторный POST после UNKNOWN не выполнялся |
| UI confirmation/refusal/stale/UNKNOWN | Проверено в browser fixtures | Не называется LIVE browser validation реального seller login |

Для финансового POST требуются явные owner choices: действующий Commission/None contract с совпадающими organization/agent/currency, подтверждённые price basis/rate/VAT, product mappings, return treatment и component policy. Источник возвращает банковское софинансирование и отрицательное производное вознаграждение; отсутствие решения по ним нельзя скрыть полным импортом.

Классификация monthly source содержит6096 component slots:1524 REFERENCE,1044 ZERO,3528 UNREPRESENTED; unknown components0. ZERO/REFERENCE — нейтральные/справочные факты, **не native mapping**. Native mapped rows0, partial rows0, unmapped rows254: owner accounting policy отсутствует. Суммы/идентичности всех255 реально присутствующих monetary groups проверены exact decimal;253 отсутствующие/null sibling groups остались UNKNOWN, а не фиктивными нулями.
