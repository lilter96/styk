import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, params } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";

type Check = { code: string; passed: boolean; message: string };
type Target = {
  product_id: string;
  offer_id: string;
  sku: string;
  warehouse_id: string;
  warehouse_name: string;
  source_item_id: string;
  source_item_name: string;
  source_parent_id: string;
  source_parent_name: string;
  source_warehouse_id: string;
  source_warehouse_name: string;
  uncertain_write: boolean;
  expired_evidence: boolean;
  product_mapping_confirmed: boolean;
  warehouse_mapping_confirmed: boolean;
  physical: string | null;
  reserve: string | null;
  unreflected: string | null;
  unseen_bound: string | null;
  safety_stock: string | null;
  candidate: string | null;
  marketplace_free: string | null;
  source_batch_finished: string | null;
  marketplace_batch_finished: string | null;
  source_batch_started: string | null;
  marketplace_batch_started: string | null;
  checks: Check[];
};
type Data = { targets: Target[]; checks: Check[]; state: string };
const labels: Record<string, string> = {
  MAPPINGS_REQUIRED: "Подтвердите соответствия товаров и складов",
  PRODUCT_MAPPING_NOT_CONFIRMED: "Не подтверждено соответствие товара",
  PRODUCT_MAPPING_REFERENCE_MISSING: "Не подтверждено соответствие товара",
  PRODUCT_MAPPING_CONFLICT: "Обнаружен конфликт соответствий товара",
  WAREHOUSE_MAPPING_NOT_CONFIRMED: "Не подтверждено соответствие склада",
  WAREHOUSE_MAPPING_REFERENCE_MISSING: "Не подтверждено соответствие склада",
  WAREHOUSE_MAPPING_CONFLICT: "Обнаружен конфликт соответствий склада",
  SHARED_POOL_UNSUPPORTED:
    "Запас может использоваться несколькими товарами или вариантами",
  SOURCE_OBSERVATION_MISSING: "Нет данных об остатке на складе МойСклад",
  PHYSICAL_STOCK_MISSING: "Нет данных о количестве на складе",
  SOURCE_RESERVE_MISSING: "Нет данных о резерве МойСклад",
  PHYSICAL_STOCK_UNSUPPORTED:
    "Количество на складе должно быть неотрицательным целым числом",
  SOURCE_RESERVE_UNSUPPORTED: "Резерв должен быть неотрицательным целым числом",
  INVALID_SAFETY_STOCK: "Не указан допустимый страховой запас",
  SOURCE_OBSERVATION_STALE: "Данные МойСклад устарели",
  SOURCE_TRAVERSAL_STALE: "Загрузка МойСклад началась слишком давно",
  OZON_TRAVERSAL_STALE: "Загрузка Ozon началась слишком давно",
  OUT_OF_ORDER_IMPORT:
    "Загрузки завершились в неверном порядке. Повторите загрузку",
  UNREFLECTED_OBLIGATIONS_MISSING: "Неизвестны обязательства по заказам",
  UNSEEN_REDUCTION_BOUND_MISSING: "Не подтверждены другие расходы запаса",
  CUSTOMER_ORDER_IMPORT_MISSING: "Не подтверждена полнота заказов",
  COMPETING_EXPOSURE_NOT_VERIFIED:
    "Не подтверждено распределение общего запаса",
  RESERVATION_ATTRIBUTION_UNKNOWN: "Не подтверждено распределение резервов",
  UNCERTAIN_WRITER_SETTLEMENT_REQUIRED: "Нужно уточнить предыдущую отправку",
  CATALOG_IDENTITY_CHANGED: "Проверьте соответствие товара в каталоге Ozon",
  WAREHOUSE_SCHEMA_UNCONFIRMED: "Проверьте соответствие склада Ozon",
  SYNC_CONDITIONS_CHANGED:
    "Соответствия или подключение изменились — проверьте условия заново",
  PROOF_AFTER_SOURCE_TRAVERSAL_STARTED:
    "Подтвердите условия до новой загрузки МойСклад",
  OZON_STOCK_BASELINE_REQUIRED: "Нет данных об остатке товара на складе Ozon",
  OZON_STOCK_VALUES_INVALID: "Проверьте данные об остатках Ozon",
};
const priority = [
  "PRODUCT_MAPPING_CONFLICT",
  "WAREHOUSE_MAPPING_CONFLICT",
  "CATALOG_IDENTITY_CHANGED",
  "WAREHOUSE_SCHEMA_UNCONFIRMED",
  "SHARED_POOL_UNSUPPORTED",
  "SOURCE_OBSERVATION_MISSING",
  "PHYSICAL_STOCK_MISSING",
  "SOURCE_RESERVE_MISSING",
  "PHYSICAL_STOCK_UNSUPPORTED",
  "SOURCE_RESERVE_UNSUPPORTED",
  "SOURCE_OBSERVATION_STALE",
  "SOURCE_TRAVERSAL_STALE",
  "OZON_STOCK_BASELINE_REQUIRED",
  "UNREFLECTED_OBLIGATIONS_MISSING",
  "COMPETING_EXPOSURE_NOT_VERIFIED",
];
function reasons(checks: Check[]) {
  const failed = checks.filter(
    (check) => !check.passed && check.code !== "ALL_TARGETS_READY",
  );
  failed.sort(
    (a, b) =>
      (priority.indexOf(a.code) < 0 ? 999 : priority.indexOf(a.code)) -
      (priority.indexOf(b.code) < 0 ? 999 : priority.indexOf(b.code)),
  );
  return [
    ...new Set(
      failed.map(
        (check) => labels[check.code] || "Не все условия расчёта подтверждены",
      ),
    ),
  ].slice(0, 3);
}
const value = (number: string | null) =>
  number == null || number === "" ? "Нет данных" : number;
const date = (time: string | null) =>
  time ? new Date(time).toLocaleString("ru-RU") : "Нет данных";

export function StockShadow({
  tenant,
  moy,
  ozon,
  moyLabel = "Выбранный аккаунт",
  ozonLabel = "Выбранный аккаунт",
}: {
  tenant: string;
  moy: string;
  ozon: string;
  moyLabel?: string;
  ozonLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const scope = { tenant, moysklad_account: moy, ozon_account: ozon };
  const query = useQuery({
    queryKey: ["stock-shadow", tenant, moy, ozon],
    queryFn: ({ signal }) =>
      api<Data>(`/api/stock-sync/shadow?${params(scope)}`, signal),
    enabled: open && !!moy && !!ozon,
    retry: false,
    staleTime: 0,
  });
  return (
    <>
      <button
        className="secondary"
        disabled={!moy || !ozon}
        onClick={() => setOpen(true)}
      >
        Проверить остатки
      </button>
      {open && (
        <IntegrationDialog
          title="Проверка остатков"
          className="stock-shadow-dialog"
          onClose={() => setOpen(false)}
        >
          <div className="shadow-intro">
            <p>
              МойСклад: <strong>{moyLabel}</strong> · Ozon:{" "}
              <strong>{ozonLabel}</strong>
            </p>
            <p>Проверка не изменяет остатки. Отправка в Ozon отключена.</p>
          </div>
          {query.isLoading && <p>Загружаем данные…</p>}
          {query.isError && (
            <p role="alert">
              Не удалось получить данные.{" "}
              <button
                className="text-action"
                onClick={() => void query.refetch()}
              >
                Повторить
              </button>
            </p>
          )}
          {query.data && (
            <>
              {query.data.state.includes("UNKNOWN") && (
                <p role="alert">Результат предыдущей отправки неизвестен.</p>
              )}
              {reasons(query.data.checks).map((reason) => (
                <p key={reason}>{reason}</p>
              ))}
              {!query.data.targets.length && (
                <p>Нет подтверждённых пар товара и склада.</p>
              )}
              {query.data.targets.map((t) => (
                <article
                  className="shadow-target"
                  key={`${t.product_id}:${t.warehouse_id}`}
                >
                  <div className="shadow-heading">
                    <div>
                      <h3>
                        {t.source_item_name ||
                          t.offer_id ||
                          t.sku ||
                          `Товар ${t.product_id}`}
                      </h3>
                      <p>Ozon: {t.offer_id || t.sku || t.product_id}</p>
                    </div>
                    <span className="connection-state warning">
                      {t.candidate != null
                        ? "Расчёт выполнен"
                        : "Недостаточно данных"}
                    </span>
                  </div>
                  {t.source_parent_id && (
                    <p>
                      Родительский товар:{" "}
                      {t.source_parent_name || t.source_parent_id}
                    </p>
                  )}
                  <p>
                    Склады: {t.source_warehouse_name || t.source_warehouse_id} →{" "}
                    {t.warehouse_name || t.warehouse_id}
                  </p>
                  {t.product_mapping_confirmed &&
                    t.warehouse_mapping_confirmed && (
                      <p className="shadow-times">
                        Соответствия товара и склада подтверждены
                      </p>
                    )}
                  <div className="shadow-values">
                    <div>
                      <span>На складе МойСклад</span>
                      <strong>{value(t.physical)}</strong>
                    </div>
                    <div>
                      <span>Резерв МойСклад</span>
                      <strong>{value(t.reserve)}</strong>
                    </div>
                    <div>
                      <span>Свободно в Ozon</span>
                      <strong>{value(t.marketplace_free)}</strong>
                    </div>
                    <div>
                      <span>Расчётное количество</span>
                      <strong>{value(t.candidate)}</strong>
                    </div>
                  </div>
                  <p className="shadow-times">
                    Загрузки: МойСклад — {date(t.source_batch_finished)}; Ozon —{" "}
                    {date(t.marketplace_batch_finished)}
                  </p>
                  {t.candidate != null && t.marketplace_free != null && (
                    <p>
                      Разница с Ozon:{" "}
                      {(
                        BigInt(t.candidate) - BigInt(t.marketplace_free)
                      ).toString()}
                      . Это не разрешение на отправку.
                    </p>
                  )}
                  {t.uncertain_write && (
                    <p className="shadow-reason">
                      Результат предыдущей отправки неизвестен.
                    </p>
                  )}
                  {t.expired_evidence && (
                    <p className="shadow-reason">
                      Срок подтверждения оператора истёк.
                    </p>
                  )}
                  {reasons(t.checks).map((reason) => (
                    <p className="shadow-reason" key={reason}>
                      {reason}
                    </p>
                  ))}
                  <details>
                    <summary>Технические детали</summary>
                    <p>
                      МойСклад: загрузка с {date(t.source_batch_started)} до{" "}
                      {date(t.source_batch_finished)}
                      <br />
                      Ozon: загрузка с {date(
                        t.marketplace_batch_started,
                      )} до {date(t.marketplace_batch_finished)}
                    </p>
                    <p>
                      Время каждой отдельной записи неизвестно; загрузки систем
                      не обязательно одновременны.
                    </p>
                    <p>
                      Указано оператором: обязательства — {value(t.unreflected)}
                      , страховой запас — {value(t.safety_stock)}, другие
                      расходы — {value(t.unseen_bound)}.
                    </p>
                    <p>
                      Товар Ozon: {t.product_id}; склад Ozon: {t.warehouse_id};
                      товар МойСклад: {t.source_item_id}; склад МойСклад:{" "}
                      {t.source_warehouse_id}
                    </p>
                    <p>
                      Проверки:{" "}
                      {t.checks
                        .filter((c) => !c.passed)
                        .map((c) => c.code)
                        .join(", ") || "нет"}
                    </p>
                  </details>
                </article>
              ))}
              <p>
                <Link to="/products">Сопоставить товары</Link> ·{" "}
                <Link to="/warehouses">Сопоставить склады</Link>
              </p>
              <button
                className="secondary"
                onClick={() => void query.refetch()}
              >
                Обновить проверку
              </button>
            </>
          )}
        </IntegrationDialog>
      )}
    </>
  );
}
