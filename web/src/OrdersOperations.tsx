import { OrdersNativeSummary, nativeDocumentURL } from "./OrdersNativeSummary";
import { OrdersRecovery } from "./OrdersRecovery";
import { useLabelExpiryClock } from "./useLabelExpiryClock";
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { OrdersPositionPatch } from "./OrdersPositionPatch";
import { OrdersSplitEvidence } from "./OrdersSplitEvidence";
import {
  OrdersActionSummary,
  safeOrderArtifactURL,
} from "./OrdersActionSummary";
import {
  MoyOrderLink,
  orderActionLabel,
  orderDate,
  orderRequestError,
  ordersURL,
  orderStateLabels,
  sameOrderScope,
  type OrderScope,
  type OrdersOperationPage,
} from "./OrdersTypes";

export function OrdersOperations({
  scope,
  onLinks,
  onOutcomes,
  onRecoveryAuthorization,
}: {
  scope: OrderScope;
  onLinks?: (links: Record<string, string>) => void;
  onOutcomes?: (states: Record<string, string>) => void;
  onRecoveryAuthorization?: (
    posting: string,
    until: string | undefined,
  ) => void;
}) {
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors[cursors.length - 1];
  const history = useQuery({
    queryKey: ["orders-operations", scope, cursor],
    queryFn: async ({ signal }) => {
      const value = await api<OrdersOperationPage>(
        ordersURL("operations", scope, { cursor, limit: "25" }),
        signal,
      );
      if (
        (value.operations ?? []).some(
          (item) => !sameOrderScope(item.scope, scope),
        )
      )
        throw new Error("operation scope mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (query) =>
      (query.state.data?.operations ?? []).some((operation) =>
        (operation.targets ?? []).some((work) =>
          [
            "QUEUED",
            "CLAIMED",
            "DISPATCHING",
            "ACKNOWLEDGED",
            "POLLING",
            "UNKNOWN",
          ].includes(work.state),
        ),
      )
        ? 10000
        : false,
  });
  const labelNow = useLabelExpiryClock(history.data);
  useEffect(() => {
    const links: Record<string, string> = {};
    const states: Record<string, string> = {};
    for (const operation of history.data?.operations ?? [])
      for (const work of operation.targets ?? []) {
        const id =
          work.command.correlation?.customer_order_id ||
          (work.command.action.startsWith("mirror_")
            ? work.receipt?.external_id
            : undefined);
        if (id) links[work.command.posting_number] = id;
        states[work.command.posting_number] ??= work.state;
      }
    onLinks?.(links);
    onOutcomes?.(states);
  }, [history.data, onLinks, onOutcomes]);
  return (
    <section
      className="orders-section"
      aria-labelledby="orders-history-heading"
    >
      <div className="orders-section-heading">
        <h2 id="orders-history-heading">История передачи</h2>
        <button
          type="button"
          className="secondary"
          disabled={history.isFetching}
          onClick={() => void history.refetch()}
        >
          Обновить статусы заказов
        </button>
      </div>
      <p className="orders-note">
        Принятие запроса ещё не подтверждает изменения в сервисах. Итог —
        «Подтверждено сверкой» для каждого отправления.
      </p>
      {history.isLoading && <p role="status">Читаем историю…</p>}
      {history.isError && (
        <p role="alert" className="orders-error">
          {orderRequestError(history.error)}
        </p>
      )}
      {!history.isLoading &&
        !history.isError &&
        !history.data?.operations?.length && (
          <p className="orders-empty">
            Передач пока нет. Выберите отправления и проверьте изменения перед
            подтверждением.
          </p>
        )}
      {(history.data?.operations ?? []).map((operation) => (
        <article className="orders-posting" key={operation.id}>
          <div className="orders-section-heading">
            <h3>Передача от {orderDate(operation.created_at)}</h3>
            <span className="orders-badge">
              {orderStateLabels[operation.state] ?? operation.state}
            </span>
          </div>
          {(operation.targets ?? []).map((work) => (
            <div className="orders-work" key={work.id}>
              <div>
                <strong>{work.command.posting_number}</strong>
                <span
                  className={`orders-badge ${work.state === "UNKNOWN" || work.state === "BLOCKED" ? "warning" : work.state === "RECONCILED" ? "success" : ""}`}
                >
                  {work.command.action === "posting_country_set" &&
                  work.state === "RECONCILED"
                    ? "Требование страны снято"
                    : (orderStateLabels[work.state] ?? work.state)}
                </span>
              </div>
              <p>
                {orderActionLabel(work.command.action)} ·{" "}
                {work.automatic
                  ? "По разрешению автоматизации"
                  : "Подтверждено вручную"}
              </p>
              {work.command.position_patch && (
                <OrdersPositionPatch value={work.command.position_patch} />
              )}
              {work.command.action.startsWith("mirror_") && (
                <MoyOrderLink
                  id={
                    work.command.correlation?.customer_order_id ||
                    work.receipt?.external_id
                  }
                />
              )}
              {work.command.native_movement && (
                <details>
                  <summary>Подтверждённый складской документ</summary>
                  <OrdersNativeSummary command={work.command} />
                  {nativeDocumentURL(
                    work.command,
                    work.native_document?.id ?? work.receipt?.external_id,
                  ) && (
                    <a
                      href={nativeDocumentURL(
                        work.command,
                        work.native_document?.id ?? work.receipt?.external_id,
                      )}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      Открыть складской документ МойСклад
                    </a>
                  )}
                </details>
              )}
              {work.command.posting_action && (
                <details>
                  <summary>Подтверждённые параметры действия</summary>
                  <OrdersActionSummary command={work.command} />
                </details>
              )}
              {work.receipt?.country_evidence && (
                <p className="orders-note">
                  SKU {work.receipt.country_evidence.sku}:{" "}
                  {work.receipt.country_evidence.requirement_resolved
                    ? "требование указать страну снято"
                    : "снятие требования не подтверждено"}
                  . Установленное значение страны отдельным чтением не
                  подтверждено.
                </p>
              )}
              {(work.receipt?.task_ids ?? []).map((task, index) => (
                <p className="orders-note" key={`${task.id}:${index}`}>
                  Задача обработки Ozon: {task.id}
                </p>
              ))}
              {["posting_split", "posting_split_unassembled"].includes(
                work.command.action,
              ) && <OrdersSplitEvidence receipt={work.receipt} />}
              {work.state === "RECONCILED" &&
                work.receipt?.artifact?.available &&
                (!work.receipt.artifact.expires_at ||
                  Date.parse(work.receipt.artifact.expires_at) > labelNow) &&
                safeOrderArtifactURL(work.receipt.artifact.download_url) && (
                  <a
                    href={safeOrderArtifactURL(
                      work.receipt.artifact.download_url,
                    )}
                  >
                    Скачать этикетку
                  </a>
                )}
              {work.state === "RECONCILED" &&
                work.receipt?.artifact?.expires_at && (
                  <p className="orders-note">
                    {Date.parse(work.receipt.artifact.expires_at) > labelNow
                      ? `Этикетка доступна до ${orderDate(work.receipt.artifact.expires_at)}.`
                      : "Срок хранения этикетки истёк."}
                  </p>
                )}
              {work.state === "UNKNOWN" && (
                <p className="orders-warning">
                  Запрос мог выполниться. STYK сверяет результат чтением;
                  повторная отправка автоматически не выполняется. Обновите
                  статусы и дождитесь подтверждения.
                </p>
              )}
              {work.state === "UNKNOWN" && (
                <OrdersRecovery
                  scope={scope}
                  work={work}
                  onAuthorization={onRecoveryAuthorization}
                />
              )}
              {work.state === "BLOCKED" && (
                <p className="orders-warning">
                  Условия передачи изменились. Проверьте настройки и связи,
                  затем создайте новый предпросмотр.
                </p>
              )}
              {(work.code || work.receipt?.code) && (
                <details>
                  <summary>Сведения об операции</summary>
                  <p>
                    Код: <code>{work.code || work.receipt.code}</code>
                  </p>
                  <p>ID: {work.id}</p>
                </details>
              )}
            </div>
          ))}
        </article>
      ))}
      <div className="orders-actions">
        <button
          className="secondary"
          disabled={history.isFetching || cursors.length === 1}
          onClick={() => setCursors((value) => value.slice(0, -1))}
        >
          Предыдущие операции
        </button>
        <span>Страница {cursors.length}</span>
        <button
          className="secondary"
          disabled={history.isFetching || !history.data?.next_cursor}
          onClick={() =>
            setCursors((value) => [...value, history.data!.next_cursor!])
          }
        >
          Следующие операции
        </button>
      </div>
    </section>
  );
}
