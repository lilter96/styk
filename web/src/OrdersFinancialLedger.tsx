import { useState } from "react";
import { orderFieldText, type OrderField } from "./OrdersTypes";

export type FinancialLedger = {
  version: string;
  source_fingerprint: string;
  period: { year: number; month: number };
  currency: string;
  rows_total: number;
  rows_classified: number;
  rows_mapped_to_native: number;
  rows_partial: number;
  rows_unmapped: number;
  components_total: number;
  components_represented: number;
  components_unrepresented: number;
  unknown_components: number;
  complete: boolean;
  rows:
    | {
        source_row: number;
        classification: string;
        native_status: string;
        blockers: string[] | null;
      }[]
    | null;
  components:
    | {
        id: string;
        source_row: number;
        side: string;
        component: string;
        category: string;
        role: string;
        value: OrderField<string>;
        currency: string;
        sign: string;
        disposition: string;
        blocker?: string;
        source_presence?: string;
      }[]
    | null;
  category_totals:
    | {
        category: string;
        side: string;
        role: string;
        currency: string;
        sum: string;
        known: number;
        missing: number;
        null: number;
        positive: number;
        negative: number;
        zero: number;
      }[]
    | null;
  reconciliation:
    | {
        source_row: number;
        side: string;
        component_formula?: string;
        component_expected_net?: OrderField<string>;
        component_difference?: OrderField<string>;
        component_status?: string;
        gross: OrderField<string>;
        reward: OrderField<string>;
        expected_net: OrderField<string>;
        source_net: OrderField<string>;
        difference: OrderField<string>;
        status: string;
      }[]
    | null;
};
const labels: Record<string, string> = {
  SALE: "Продажа",
  RETURN: "Финансовый возврат",
  MIXED: "Продажа и возврат",
  ZERO: "Явный ноль",
  UNKNOWN: "Неизвестно",
  ADJUSTMENT: "Корректировка",
  ROW: "Строка",
  HEADER: "Заголовок",
  SALE_GROSS: "Сумма продаж",
  RETURN_GROSS: "Сумма финансовых возвратов",
  COMMISSION_FEE: "Базовое вознаграждение Ozon",
  DISCOUNT_POINTS: "Баллы за скидки",
  BANK_COINVESTMENT: "Банковское софинансирование",
  PICKUP_COINVESTMENT: "Компонент pick_up_point_coinvestment",
  LOYALTY: "Компонент stars",
  LEGACY_COMMISSION: "Историческая комиссия",
  COMPENSATION: "Компенсации",
  SALE_PRICE: "Цена реализации",
  SELLER_PRICE: "Цена продавца",
  QUANTITY: "Количество",
  COMMISSION_RATIO: "Коэффициент комиссии",
  CONTROL_TOTAL: "Контрольная сумма источника",
  REFERENCE: "Справочное значение",
  NATIVE_SALE: "В позиции продажи черновика",
  NATIVE_RETURN: "В позиции возврата черновика",
  NATIVE_REWARD: "В вознаграждении черновика",
  UNREPRESENTED: "Не включено в черновик",
  UNRECOGNIZED: "Неизвестный компонент",
  UNMAPPED: "Не включена",
  PARTIAL: "Включена частично",
  MAPPED: "Включена в план",
  MATCH: "Сходится",
  DIFFERENCE: "Есть расхождение",
  UNIT_PRICE: "Цена за единицу",
  PERCENT: "Процент",
  BASE: "Основание",
  NOT_APPLICABLE: "Не применяется в этом периоде",
  POSITIVE: "Положительное значение",
  NEGATIVE: "Отрицательное значение",
};
const label = (value: string) => labels[value] ?? value;
const unit = (role: string, currency: string) =>
  ["QUANTITY", "PERCENT"].includes(role) ? "" : currency;
function valueText(value: OrderField<string>) {
  if (value.presence === "missing") return "Поле отсутствует";
  if (value.presence === "null") return "NULL — значение неизвестно";
  return orderFieldText(value);
}
export function OrdersFinancialLedger({
  ledger,
  planned = false,
}: {
  ledger: FinancialLedger;
  planned?: boolean;
}) {
  const [page, setPage] = useState(0);
  const [filter, setFilter] = useState("all");
  const [componentPage, setComponentPage] = useState(0);
  const [reconciliationPage, setReconciliationPage] = useState(0);
  const residual = (ledger.components ?? []).filter((item) =>
    ["UNREPRESENTED", "UNRECOGNIZED"].includes(item.disposition),
  );
  const absent = residual.filter((item) =>
    ["missing", "null"].includes(item.source_presence ?? ""),
  );
  const controls = residual.filter(
    (item) => !absent.includes(item) && item.category === "CONTROL_TOTAL",
  );
  const amounts = residual.filter(
    (item) => !absent.includes(item) && !controls.includes(item),
  );
  const groups = Array.from(new Set(amounts.map((item) => item.category)));
  const all = ledger.rows ?? [];
  const filtered = all.filter(
    (row) =>
      filter === "all" ||
      row.native_status === "UNMAPPED" ||
      row.native_status === "PARTIAL" ||
      row.classification === "UNKNOWN",
  );
  const rows = filtered.slice(page * 20, (page + 1) * 20);
  return (
    <section
      className="settings-financial-ledger"
      aria-label={
        planned ? "Финансовый состав черновика" : "Финансовый состав источника"
      }
    >
      <h4>
        {planned
          ? "Финансовый состав черновика"
          : "Финансовый состав источника"}
      </h4>
      <dl className="settings-realization-facts">
        <div>
          <dt>Классифицировано строк</dt>
          <dd>
            {ledger.rows_classified} из {ledger.rows_total}
          </dd>
        </div>
        <div>
          <dt>Полностью включено в план</dt>
          <dd>{ledger.rows_mapped_to_native}</dd>
        </div>
        <div>
          <dt>Частично / не включено</dt>
          <dd>
            {ledger.rows_partial} / {ledger.rows_unmapped}
          </dd>
        </div>
        <div>
          <dt>Без проекции в документ</dt>
          <dd>
            {ledger.components_unrepresented} из {ledger.components_total}
          </dd>
        </div>
        <div>
          <dt>Неизвестных компонентов</dt>
          <dd>{ledger.unknown_components}</dd>
        </div>
      </dl>
      {!planned && (
        <section aria-label="Что означает счётчик компонентов">
          <h4>Что означает счётчик компонентов</h4>
          <p>
            Это не число ошибок и не число документов. План переноса ещё не
            подтверждён.
          </p>
          <ul>
            <li>
              Поля отсутствующих блоков источника: {absent.length}. Ozon не
              передал эти блоки; это не нулевые суммы и не отдельные
              бухгалтерские ошибки.
            </li>
            <li>
              Контрольные суммы для сверки: {controls.length}. Они не создают
              дополнительные позиции документа.
            </li>
            <li>Компоненты для проверки переноса: {amounts.length}.</li>
          </ul>
          <ul>
            {groups.map((category) => (
              <li key={category}>
                {label(category)}:{" "}
                {amounts.filter((item) => item.category === category).length}.
                {category === "BANK_COINVESTMENT" &&
                  " Нужна отдельная бухгалтерская обработка: этот компонент не включается автоматически в комиссию или цену."}
              </li>
            ))}
          </ul>
          <p>
            Продажи, базовая комиссия и баллы проверяются при построении плана
            по договору, цене, НДС и связям товаров. Их наличие здесь само по
            себе не означает, что API МойСклад их не поддерживает.
          </p>
        </section>
      )}
      <p className={ledger.complete ? "settings-info" : "settings-warning"}>
        {ledger.complete
          ? "Состав поддерживаемого плана сверён. Это не подтверждение выплаты или создания документа."
          : "Финансовая сверка не завершена. Проверьте денежные компоненты и условия создания ниже. Отсутствующие блоки источника не считаются отдельными ошибками."}
      </p>
      <p>
        Справочные цены, количество и контрольные итоги не складываются в общую
        сумму. Финансовый возврат не создаёт складской Возврат покупателя.
        Показана оценка по текущим правилам, а не факт отправки в МойСклад.
      </p>
      <details>
        <summary>
          Неучтённые и неизвестные компоненты ({residual.length})
        </summary>
        <ul className="settings-financial-components">
          {residual
            .slice(componentPage * 30, (componentPage + 1) * 30)
            .map((item) => (
              <li key={item.id}>
                <strong>
                  {item.source_row > 0
                    ? `Строка ${item.source_row}`
                    : "Заголовок"}
                  : {label(item.category)}
                </strong>
                <span>
                  {label(item.side)} · {item.component}: {valueText(item.value)}{" "}
                  {unit(item.role, item.currency)}
                </span>
                <span>
                  {label(item.sign)}; {label(item.disposition)}.
                </span>
                {item.blocker && (
                  <span>
                    Причина: <code>{item.blocker}</code>
                  </span>
                )}
              </li>
            ))}
        </ul>
        {residual.length === 0 ? (
          <p>
            В этой классификации неучтённых компонентов нет. Проверьте остальные
            условия и бухгалтерскую политику отдельно.
          </p>
        ) : (
          <div
            className="settings-pagination"
            aria-label="Страницы неучтённых компонентов"
          >
            <button
              type="button"
              className="secondary"
              disabled={componentPage === 0}
              onClick={() => setComponentPage(componentPage - 1)}
            >
              Предыдущие компоненты
            </button>
            <span>
              {componentPage * 30 + 1}–
              {Math.min((componentPage + 1) * 30, residual.length)} из{" "}
              {residual.length}
            </span>
            <button
              type="button"
              className="secondary"
              disabled={(componentPage + 1) * 30 >= residual.length}
              onClick={() => setComponentPage(componentPage + 1)}
            >
              Следующие компоненты
            </button>
          </div>
        )}
      </details>
      <details>
        <summary>Итоги по компонентам и знак значений</summary>
        <ul className="settings-realization-rows">
          {(ledger.category_totals ?? []).map((total, index) => (
            <li key={`${total.category}:${total.side}:${total.role}:${index}`}>
              <strong>
                {label(total.category)} · {label(total.side)}
              </strong>
              <span>
                {label(total.role)}: {total.sum}{" "}
                {unit(total.role, total.currency)}
              </span>
              <span>
                Известно: {total.known}; отсутствует: {total.missing}; NULL:{" "}
                {total.null}.
              </span>
              <span>
                Положительных: {total.positive}; отрицательных: {total.negative}
                ; явных нулей: {total.zero}.
              </span>
            </li>
          ))}
        </ul>
      </details>
      <details>
        <summary>Построчная классификация и компоненты</summary>
        <label>
          Показать строки финансового состава
          <select
            value={filter}
            onChange={(event) => {
              setFilter(event.target.value);
              setPage(0);
            }}
          >
            <option value="all">Все строки</option>
            <option value="review">Требуют разбора</option>
          </select>
        </label>
        <ul className="settings-realization-rows">
          {rows.map((row) => (
            <li key={row.source_row}>
              <strong>
                Строка {row.source_row}: {label(row.classification)}
              </strong>
              <span>{label(row.native_status)}</span>
              <details>
                <summary>Компоненты строки {row.source_row}</summary>
                <ul className="settings-financial-components">
                  {(ledger.components ?? [])
                    .filter((item) => item.source_row === row.source_row)
                    .map((item) => (
                      <li key={item.id}>
                        <strong>
                          {label(item.category)} · {label(item.side)}
                        </strong>
                        <span>
                          {item.component}: {valueText(item.value)}{" "}
                          {unit(item.role, item.currency)}
                        </span>
                        <span>
                          {label(item.role)}; {label(item.disposition)}.
                        </span>
                        {item.blocker && (
                          <span>
                            Требует разбора: <code>{item.blocker}</code>
                          </span>
                        )}
                      </li>
                    ))}
                </ul>
              </details>
              {(row.blockers ?? []).length > 0 && (
                <span>Причины: {(row.blockers ?? []).join(", ")}</span>
              )}
            </li>
          ))}
        </ul>
        {filtered.length === 0 && (
          <p>
            Строк по этому фильтру нет. Выберите «Все строки» для полного
            состава.
          </p>
        )}
        <div
          className="settings-pagination"
          aria-label="Страницы финансового состава"
        >
          <button
            type="button"
            className="secondary"
            disabled={page === 0}
            onClick={() => setPage(page - 1)}
          >
            Предыдущие строки
          </button>
          <span>
            {filtered.length
              ? `${page * 20 + 1}–${Math.min((page + 1) * 20, filtered.length)} из ${filtered.length}`
              : "0 строк"}
          </span>
          <button
            type="button"
            className="secondary"
            disabled={(page + 1) * 20 >= filtered.length}
            onClick={() => setPage(page + 1)}
          >
            Следующие строки
          </button>
        </div>
      </details>
      <details>
        <summary>
          Проверка сумм строк ({(ledger.reconciliation ?? []).length})
        </summary>
        <ul className="settings-realization-rows">
          {(ledger.reconciliation ?? [])
            .slice(reconciliationPage * 30, (reconciliationPage + 1) * 30)
            .map((item, index) => (
              <li key={`${item.source_row}:${item.side}:${index}`}>
                <strong>
                  Строка {item.source_row} · {label(item.side)}:{" "}
                  {label(item.status)}
                </strong>
                <span>
                  Сумма: {valueText(item.gross)}; вознаграждение:{" "}
                  {valueText(item.reward)}.
                </span>
                <span>
                  Ожидаемое начисление: {valueText(item.expected_net)};
                  источник: {valueText(item.source_net)}.
                </span>
                <span>
                  Разница: {valueText(item.difference)} {ledger.currency}.
                </span>
                {item.component_status && (
                  <span>
                    Проверка по компонентам: {label(item.component_status)}.
                    Ожидаемое начисление:{" "}
                    {item.component_expected_net
                      ? valueText(item.component_expected_net)
                      : "Нет данных"}
                    ; разница:{" "}
                    {item.component_difference
                      ? valueText(item.component_difference)
                      : "Нет данных"}{" "}
                    {ledger.currency}.{" "}
                    {item.component_formula &&
                      `Основание: ${item.component_formula}.`}
                  </span>
                )}
              </li>
            ))}
        </ul>
        {(ledger.reconciliation?.length ?? 0) > 30 && (
          <div
            className="settings-pagination"
            aria-label="Страницы проверки сумм"
          >
            <button
              type="button"
              className="secondary"
              disabled={reconciliationPage === 0}
              onClick={() => setReconciliationPage(reconciliationPage - 1)}
            >
              Предыдущие проверки
            </button>
            <span>
              {reconciliationPage * 30 + 1}–
              {Math.min(
                (reconciliationPage + 1) * 30,
                ledger.reconciliation?.length ?? 0,
              )}{" "}
              из {ledger.reconciliation?.length}
            </span>
            <button
              type="button"
              className="secondary"
              disabled={
                (reconciliationPage + 1) * 30 >=
                (ledger.reconciliation?.length ?? 0)
              }
              onClick={() => setReconciliationPage(reconciliationPage + 1)}
            >
              Следующие проверки
            </button>
          </div>
        )}
      </details>
      <p>
        Версия классификации: {ledger.version}. Источник:{" "}
        <code title={ledger.source_fingerprint}>
          {ledger.source_fingerprint.slice(0, 12)}…
        </code>
      </p>
    </section>
  );
}
