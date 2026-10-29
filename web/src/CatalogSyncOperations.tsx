import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  catalogURL,
  fieldLabel,
  issueLabel,
  productName,
  providerLabel,
  requestError,
  type CatalogWork,
  type OperationPage,
  type Scope,
} from "./CatalogSyncTypes";

const stateLabels: Record<string, string> = {
  QUEUED: "Ожидает отправки",
  CLAIMED: "Проверяем условия отправки",
  DISPATCHING: "Отправляем изменения",
  PROCESSING: "Сервис обрабатывает запрос",
  ACKNOWLEDGED: "Принято сервисом, сверяем данные",
  UNKNOWN: "Результат неизвестен",
  RECONCILED: "Изменения подтверждены сверкой",
  REJECTED: "Изменения отклонены",
  BLOCKED: "Отправка заблокирована",
};
const stateLabel = (state: string) =>
  stateLabels[state] || "Требуется проверка состояния";
const activeStates = new Set([
  "QUEUED",
  "CLAIMED",
  "DISPATCHING",
  "PROCESSING",
  "ACKNOWLEDGED",
]);

function targetCaption(work: CatalogWork) {
  const target = work.command.target;
  const createdID = target.id
    ? ""
    : work.created_target?.id || work.receipt.external_id;
  const primary = target.offer_id || target.id || createdID || "Новый товар";
  return createdID && target.offer_id
    ? `${primary} · ID ${createdID}`
    : primary;
}

function WorkResult({ work }: { work: CatalogWork }) {
  const errors = [
    ...(work.receipt.errors || []),
    ...(work.receipt.products || []).flatMap((product) => product.errors || []),
  ];
  const uncertain = work.state === "UNKNOWN";
  return (
    <article className="catalog-work">
      <header>
        <h4>{productName(work.decision.source)}</h4>
        <span
          className={`catalog-state ${work.state === "RECONCILED" ? "success" : uncertain || work.state === "REJECTED" || work.state === "BLOCKED" ? "warning" : ""}`}
        >
          {stateLabel(work.state)}
        </span>
      </header>
      <p>
        {providerLabel(work.command.target.account.provider)}:{" "}
        {targetCaption(work)}
      </p>
      {uncertain && (
        <p className="catalog-warning">
          Запрос мог выполниться. Повторная отправка недоступна до сверки
          результата. Обновление статуса только читает данные.
        </p>
      )}
      {(work.state === "PROCESSING" || work.state === "ACKNOWLEDGED") && (
        <p>
          Принятие запроса ещё не подтверждает изменение товара. STYK ожидает
          результат и сверяет данные.
        </p>
      )}
      {work.receipt.task_id && (
        <p>Номер обработки в сервисе: {work.receipt.task_id}</p>
      )}
      {errors.map((issue, index) => (
        <p className="catalog-error" key={`${issue.field}:${index}`}>
          {fieldLabel(issue.field)}: {issueLabel(issue.code)}
        </p>
      ))}
      {(work.state === "REJECTED" || work.state === "BLOCKED") &&
        !errors.length && (
          <p className="catalog-warning">
            Проверьте актуальные данные и настройки. Для отправки требуется
            новая проверка и подтверждение.
          </p>
        )}
    </article>
  );
}

export function CatalogSyncOperations({ scope }: { scope: Scope }) {
  const [cursor, setCursor] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const query = useQuery({
    queryKey: ["catalog-operations", scope, cursor],
    queryFn: async ({ signal }) => {
      const value = await api<OperationPage>(
        catalogURL("operations", scope, { cursor, limit: "20" }),
        signal,
      );
      return {
        ...value,
        operations: (value.operations || []).map((operation) => ({
          ...operation,
          targets: operation.targets || [],
        })),
      };
    },
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.operations.some((operation) =>
        operation.targets.some((work) => activeStates.has(work.state)),
      )
        ? 5000
        : false,
  });
  return (
    <section
      className="catalog-section"
      aria-labelledby="catalog-operations-title"
    >
      <div className="catalog-section-heading">
        <h2 id="catalog-operations-title">Отправка и сверка результатов</h2>
        <button
          className="secondary"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          Обновить статусы
        </button>
      </div>
      {query.isFetching && <p role="status">Читаем статусы операций…</p>}
      {query.isError && (
        <p className="catalog-error" role="alert">
          {requestError(query.error)}
        </p>
      )}
      {query.data && (
        <>
          {!query.data.operations.length && (
            <p className="catalog-empty">
              Отправок пока нет. Сначала проверьте изменения и подтвердите
              выбранные товары.
            </p>
          )}
          {query.data.operations.map((operation) => (
            <section
              className="catalog-operation"
              aria-label={`Отправка ${operation.id}`}
              key={operation.id}
            >
              <header>
                <p>
                  Отправка от{" "}
                  {new Date(operation.created_at).toLocaleString("ru-RU")}
                </p>
                <span>{stateLabel(operation.state)}</span>
              </header>
              {operation.targets.map((work) => (
                <WorkResult work={work} key={work.id} />
              ))}
              {!operation.targets.length && (
                <p>Данные по товарам пока не получены. Обновите статусы.</p>
              )}
            </section>
          ))}
          <div className="catalog-actions">
            <button
              className="secondary"
              disabled={!history.length || query.isFetching}
              onClick={() => {
                setCursor(history[history.length - 1]);
                setHistory(history.slice(0, -1));
              }}
            >
              Предыдущие отправки
            </button>
            <button
              className="secondary"
              disabled={!query.data.next_cursor || query.isFetching}
              onClick={() => {
                setHistory([...history, cursor]);
                setCursor(query.data!.next_cursor!);
              }}
            >
              Следующие отправки
            </button>
          </div>
        </>
      )}
    </section>
  );
}
