import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  orderDate,
  orderFieldText,
  orderRequestError,
  ordersURL,
  sameOrderScope,
  type NativeScope,
  type OrderField,
  type OrderScope,
} from "./OrdersTypes";
export type AccrualReport = {
  scope: NativeScope;
  source_family: "ACCRUAL";
  kind: "TYPES" | "BY_DAY" | "POSTINGS";
  request: { kind: string; date?: string; posting_numbers?: string[] };
  facts: {
    path: string;
    kind: string;
    presence: OrderField<unknown>["presence"];
    text?: string;
    number?: string;
    size?: number;
  }[];
  unknown_fields: {
    path: string;
    kind: string;
    value: OrderField<string>;
    value_digest?: string;
  }[];
  complete: boolean;
  pages: number;
  record_count: number;
  fingerprint: string;
  observed_at: string;
  blockers: { code: string; field?: string }[];
};
export function OrdersAccrualReport({ scope }: { scope: OrderScope }) {
  const maximum = new Date().toISOString().slice(0, 10);
  const [date, setDate] = useState(maximum);
  const [requested, setRequested] = useState<{
    kind: "TYPES" | "BY_DAY";
    date: string;
  } | null>(null);
  const [page, setPage] = useState(0);
  const report = useQuery({
    queryKey: ["orders-accrual-report", scope, requested],
    enabled: !!requested,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const value = await api<AccrualReport>(
        ordersURL(
          `finance/accrual/${requested!.kind === "TYPES" ? "types" : "by-day"}`,
          scope,
          requested!.kind === "BY_DAY" ? { date: requested!.date } : undefined,
        ),
        signal,
      );
      if (
        !sameOrderScope(value.scope, scope) ||
        value.source_family !== "ACCRUAL" ||
        value.kind !== requested!.kind ||
        !Array.isArray(value.facts) ||
        (requested!.kind === "BY_DAY" && value.request.date !== requested!.date)
      )
        throw new Error("accrual source scope mismatch");
      return value;
    },
  });
  function read(kind: "TYPES" | "BY_DAY") {
    if (report.isFetching) return;
    setPage(0);
    if (requested?.kind === kind && requested.date === date)
      void report.refetch();
    else setRequested({ kind, date });
  }
  const validDate =
    /^\d{4}-\d{2}-\d{2}$/.test(date) && date >= "2022-01-01" && date <= maximum;
  return (
    <div className="settings-card settings-accrual">
      <h3>Начисления Ozon — отдельный источник</h3>
      <p>
        Начисления не прибавляются к отчёту о реализации и не подтверждают
        выплату. Здесь только чтение и сверка исходных полей; финансовые или
        складские документы не создаются.
      </p>
      <div className="settings-realization-controls">
        <label>
          Дата начислений
          <input
            type="date"
            min="2022-01-01"
            max={maximum}
            value={date}
            disabled={report.isFetching}
            onChange={(event) => setDate(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="secondary"
          disabled={!validDate || report.isFetching}
          onClick={() => read("BY_DAY")}
        >
          Прочитать начисления за день
        </button>
        <button
          type="button"
          className="secondary"
          disabled={report.isFetching}
          onClick={() => read("TYPES")}
        >
          Прочитать типы начислений
        </button>
      </div>
      {!validDate && (
        <p role="alert">
          Выберите дату с 1 января 2022 года не позднее сегодняшнего дня.
        </p>
      )}
      {report.isFetching && (
        <p role="status">Последовательно читаем источник начислений Ozon…</p>
      )}
      {report.isError && <p role="alert">{orderRequestError(report.error)}</p>}
      {report.data && (
        <section aria-label="Источник начислений Ozon">
          <dl className="settings-realization-facts">
            <div>
              <dt>Вид источника</dt>
              <dd>
                {report.data.kind === "TYPES"
                  ? "Справочник типов начислений"
                  : "Начисления за день"}
              </dd>
            </div>
            {report.data.request.date && (
              <div>
                <dt>Дата источника</dt>
                <dd>{report.data.request.date}</dd>
              </div>
            )}
            <div>
              <dt>Записей источника</dt>
              <dd>{report.data.record_count}</dd>
            </div>
            <div>
              <dt>Покрытие чтения</dt>
              <dd>
                {report.data.complete
                  ? "Обход ответа завершён"
                  : "Чтение неполное — требуется проверка"}
                ; страниц: {report.data.pages}
              </dd>
            </div>
            <div>
              <dt>Проверено</dt>
              <dd>{orderDate(report.data.observed_at)}</dd>
            </div>
          </dl>
          {report.data.record_count === 0 && (
            <p className="settings-info">
              В ответе нет записей. Это не доказывает нулевую выручку,
              отсутствие других начислений или выплату.
            </p>
          )}
          {(report.data.blockers ?? []).map((item, index) => (
            <p className="settings-warning" key={`${item.code}:${index}`}>
              Источник требует отдельной проверки: <code>{item.code}</code>. Эти
              данные не включены в документы МойСклад.
            </p>
          ))}
          <details>
            <summary>Поля источника ({report.data.facts.length})</summary>
            <ul className="settings-realization-rows">
              {report.data.facts
                .slice(page * 50, (page + 1) * 50)
                .map((fact, index) => (
                  <li key={`${fact.path}:${index}`}>
                    <strong>{fact.path}</strong>
                    <span>
                      {fact.presence === "missing"
                        ? "Поле отсутствует"
                        : fact.presence === "null"
                          ? "NULL — значение неизвестно"
                          : fact.presence === "empty"
                            ? "Явно пустое значение"
                            : (fact.text ??
                              fact.number ??
                              (fact.size !== undefined
                                ? `Элементов: ${fact.size}`
                                : "Значение не предоставлено"))}
                    </span>
                    <span>Тип источника: {fact.kind}.</span>
                  </li>
                ))}
            </ul>
            <div
              className="settings-pagination"
              aria-label="Страницы полей начислений"
            >
              <button
                type="button"
                className="secondary"
                disabled={page === 0}
                onClick={() => setPage(page - 1)}
              >
                Предыдущие поля
              </button>
              <span>
                {report.data.facts.length
                  ? `${page * 50 + 1}–${Math.min((page + 1) * 50, report.data.facts.length)} из ${report.data.facts.length}`
                  : "0 полей"}
              </span>
              <button
                type="button"
                className="secondary"
                disabled={(page + 1) * 50 >= report.data.facts.length}
                onClick={() => setPage(page + 1)}
              >
                Следующие поля
              </button>
            </div>
          </details>
          {(report.data.unknown_fields ?? []).length > 0 && (
            <details>
              <summary>
                Новые поля источника ({report.data.unknown_fields.length})
              </summary>
              <ul className="settings-realization-rows">
                {report.data.unknown_fields.map((field, index) => (
                  <li key={`${field.path}:${index}`}>
                    <strong>{field.path}</strong>
                    <span>
                      {field.kind === "OPAQUE"
                        ? "Нечисловое содержимое сохранено только контрольной суммой"
                        : field.value.presence === "null"
                          ? "NULL — значение неизвестно"
                          : orderFieldText(field.value)}
                    </span>
                    <span>
                      Требуется проверка смысла поля; в бухгалтерскую сумму оно
                      не добавляется.
                    </span>
                  </li>
                ))}
              </ul>
            </details>
          )}
          <p>
            Источник:{" "}
            <code title={report.data.fingerprint}>
              {report.data.fingerprint.slice(0, 12)}…
            </code>
            . Полнота чтения не означает завершённую финансовую сверку.
          </p>
        </section>
      )}
    </div>
  );
}
