import {
  OrdersFinancialLedger,
  type FinancialLedger,
} from "./OrdersFinancialLedger";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "./api";
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

type Amounts = {
  quantity: OrderField<string>;
  amount: OrderField<string>;
  total: OrderField<string>;
};
export type OrdersRealizationData = {
  scope: NativeScope;
  period: { year: number; month: number };
  number: string;
  currency: string;
  observed_at: string;
  fingerprint: string;
  rows: {
    number: number;
    sku: OrderField<string>;
    offer_id: OrderField<string>;
    delivery: Amounts;
    return: Amounts;
  }[];
  blockers: { field?: string; code: string }[];
  analysis?: FinancialLedger;
};

function reportError(error: unknown): string {
  if (error instanceof ApiError) {
    const messages: Record<string, string> = {
      REALIZATION_PERMISSION_REQUIRED:
        "Ozon не разрешил чтение отчёта. Проверьте права ключа API.",
      REALIZATION_SOURCE_NOT_FOUND:
        "Ozon не нашёл отчёт за выбранный период. Проверьте период и наличие отчёта в кабинете Ozon.",
      REALIZATION_RATE_LIMITED:
        "Ozon ограничил частоту запросов. Повторите проверку позже.",
      REALIZATION_PROVIDER_UNAVAILABLE:
        "Отчёт Ozon сейчас недоступен. Повторите проверку позже.",
    };
    if (error.code && messages[error.code]) return messages[error.code];
  }
  return orderRequestError(error);
}

export function OrdersRealizationReport({ scope }: { scope: OrderScope }) {
  const previous = new Date();
  previous.setUTCDate(1);
  previous.setUTCMonth(previous.getUTCMonth() - 1);
  const maximum = `${previous.getUTCFullYear()}-${String(previous.getUTCMonth() + 1).padStart(2, "0")}`;
  const [period, setPeriod] = useState(maximum);
  const [requested, setRequested] = useState("");
  const [page, setPage] = useState(0);
  const report = useQuery({
    queryKey: ["orders-realization-report", scope, requested],
    enabled: !!requested,
    queryFn: async ({ signal }) => {
      const [year, month] = requested.split("-");
      const value = await api<OrdersRealizationData>(
        ordersURL("finance/realization", scope, {
          year,
          month: String(Number(month)),
        }),
        signal,
      );
      if (
        !sameOrderScope(value.scope, scope) ||
        value.period.year !== Number(year) ||
        value.period.month !== Number(month) ||
        !Array.isArray(value.rows) ||
        (value.analysis &&
          (value.analysis.source_fingerprint !== value.fingerprint ||
            value.analysis.currency !== value.currency ||
            value.analysis.period.year !== value.period.year ||
            value.analysis.period.month !== value.period.month))
      )
        throw new Error("realization report scope mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 0,
    refetchOnReconnect: false,
  });
  const valid =
    /^\d{4}-\d{2}$/.test(period) && period >= "2023-08" && period <= maximum;
  const rows = report.data?.rows.slice(page * 20, (page + 1) * 20) ?? [];
  const load = () => {
    if (!valid || report.isFetching) return;
    setPage(0);
    if (period === requested) void report.refetch();
    else setRequested(period);
  };
  return (
    <div className="settings-card settings-realization">
      <h3>Месячный отчёт о реализации Ozon</h3>
      <p>
        Просмотр исходных данных закрытого периода. Непроведённый черновик
        отчёта МойСклад создаётся отдельным действием ниже, с финансовым
        разрешением и проверкой. Статус «Доставлено» не подтверждает выплату.
      </p>
      <div className="settings-realization-controls">
        <label>
          Месяц отчёта
          <input
            type="month"
            min="2023-08"
            max={maximum}
            value={period}
            onChange={(event) => setPeriod(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="secondary"
          disabled={!valid || report.isFetching}
          onClick={load}
        >
          {report.isFetching ? "Читаем отчёт…" : "Прочитать отчёт Ozon"}
        </button>
      </div>
      {!valid && (
        <p role="alert">Выберите закрытый месяц начиная с августа 2023 года.</p>
      )}
      {report.isError && <p role="alert">{reportError(report.error)}</p>}
      {report.data && (
        <>
          <dl className="settings-realization-facts">
            <div>
              <dt>Отчёт</dt>
              <dd>{report.data.number || "Номер не предоставлен"}</dd>
            </div>
            <div>
              <dt>Период</dt>
              <dd>
                {report.data.period.year}-
                {String(report.data.period.month).padStart(2, "0")}
              </dd>
            </div>
            <div>
              <dt>Валюта</dt>
              <dd>{report.data.currency || "Не предоставлена"}</dd>
            </div>
            <div>
              <dt>Получено строк</dt>
              <dd>{report.data.rows.length}</dd>
            </div>
            <div>
              <dt>Проверено</dt>
              <dd>{orderDate(report.data.observed_at)}</dd>
            </div>
          </dl>
          <p className="settings-warning">
            Требуется бухгалтерская проверка. API отчёта не подтверждает
            контрольные итоги и общее число строк, схему FBS/FBO и связь с
            конкретным отправлением. Финансовая строка возврата не доказывает
            физический возврат FBS на склад.
          </p>
          {report.data.blockers.some(
            (item) => item.code === "report_header_period_not_verified",
          ) && (
            <p className="settings-warning">
              Период не подтверждён заголовком исходного отчёта. Проверьте
              документ в кабинете Ozon.
            </p>
          )}
          {report.data.analysis && (
            <OrdersFinancialLedger
              key={report.data.fingerprint}
              ledger={report.data.analysis}
            />
          )}
          {report.data.rows.length === 0 ? (
            <p>
              В полученном ответе нет строк. Это не является подтверждением
              полноты отчёта.
            </p>
          ) : (
            <>
              <ul className="settings-realization-rows">
                {rows.map((row) => (
                  <li key={row.number}>
                    <strong>Строка {row.number}</strong>
                    <span>SKU: {orderFieldText(row.sku)}</span>
                    <span>Артикул: {orderFieldText(row.offer_id)}</span>
                    <span>
                      Продажи, количество:{" "}
                      {orderFieldText(row.delivery.quantity)}
                    </span>
                    <span>
                      Возвраты, количество:{" "}
                      {orderFieldText(row.return.quantity)}
                    </span>
                    <details>
                      <summary>Суммы исходной строки</summary>
                      <p>
                        Продажи: сумма Ozon (amount) —{" "}
                        {orderFieldText(row.delivery.amount)}; начислено Ozon
                        (total) — {orderFieldText(row.delivery.total)}.
                      </p>
                      <p>
                        Возвраты: сумма Ozon (amount) —{" "}
                        {orderFieldText(row.return.amount)}; начислено Ozon
                        (total) — {orderFieldText(row.return.total)}.
                      </p>
                      <p>
                        Все суммы указаны в валюте отчёта и не означают
                        фактическую выплату.
                      </p>
                    </details>
                  </li>
                ))}
              </ul>
              <div
                className="settings-pagination"
                aria-label="Страницы строк отчёта"
              >
                <button
                  type="button"
                  className="secondary"
                  disabled={page === 0}
                  onClick={() => setPage(page - 1)}
                >
                  Назад
                </button>
                <span>
                  Строки {page * 20 + 1}–
                  {Math.min((page + 1) * 20, report.data.rows.length)}
                </span>
                <button
                  type="button"
                  className="secondary"
                  disabled={(page + 1) * 20 >= report.data.rows.length}
                  onClick={() => setPage(page + 1)}
                >
                  Далее
                </button>
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}
