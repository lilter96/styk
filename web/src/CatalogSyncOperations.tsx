import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, post } from "./api";
import {
  catalogURL,
  fieldLabel,
  issueLabel,
  productName,
  providerLabel,
  requestError,
  scopeBody,
  type CatalogWork,
  type FieldIssue,
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
const warningMatchCode = "READBACK_MATCHED_WITH_WARNINGS";
const warningMatchLabel =
  "Выбранные поля подтверждены; предупреждения Ozon сохранены";

type ReconciliationResult = {
  id: string;
  work_id: string;
  state: string;
  code: string;
  completed: boolean;
};

function ProviderIssue({ issue }: { issue: FieldIssue }) {
  const warning = issue.severity === "warning";
  return (
    <div className={warning ? "catalog-warning" : "catalog-error"}>
      <p>
        {fieldLabel(issue.field)}:{" "}
        {warning
          ? "Предупреждение Ozon сохранено. Проверьте данные товара в сервисе."
          : issueLabel(issue.code)}
      </p>
      <details>
        <summary>
          Технические сведения {warning ? "о предупреждении" : "об ошибке"}
        </summary>
        <p>
          Код: <code>{issue.code}</code>
        </p>
        {issue.attribute_id !== undefined && (
          <p>ID характеристики: {issue.attribute_id}</p>
        )}
      </details>
    </div>
  );
}

function ReadOnlyReconciliation({
  work,
  scope,
  onRefresh,
}: {
  work: CatalogWork;
  scope: Scope;
  onRefresh: () => void;
}) {
  const lock = useRef(false);
  const [acknowledge, setAcknowledge] = useState(false);
  const [reason, setReason] = useState("Проверка результата обработки Ozon");
  const [requested, setRequested] = useState(false);
  const [message, setMessage] = useState("");
  async function reconcile() {
    if (lock.current || !acknowledge || !reason.trim()) return;
    lock.current = true;
    setRequested(true);
    setMessage("Читаем результат обработки и сверяем выбранные поля…");
    try {
      const result = await post<ReconciliationResult>(
        "/api/catalog-sync/reconcile",
        {
          ...scopeBody(scope),
          work_id: work.id,
          idempotency_key: crypto.randomUUID(),
          acknowledge: true,
          accept_warnings: true,
          reason: reason.trim(),
        },
        AbortSignal.timeout(60000),
      );
      const matched =
        result?.completed === true &&
        result.work_id === work.id &&
        result.state === "RECONCILED";
      setMessage(
        matched
          ? result.code === warningMatchCode
            ? warningMatchLabel
            : "Выбранные поля подтверждены сверкой."
          : "Сверка не подтвердила результат. Исходное состояние сохранено; проверьте обновлённую историю.",
      );
      onRefresh();
    } catch {
      setMessage(
        "Результат сверки неизвестен. Повторный запрос не отправляется. Обновите статусы: это только чтение истории.",
      );
    }
  }
  return (
    <details>
      <summary>Сверить результат без повторной отправки</summary>
      <div className="catalog-facts-body">
        <p>
          STYK только читает результат обработки в Ozon и актуальные значения
          выбранных полей. Товар повторно не отправляется.
        </p>
        <p>
          Если выбранные поля совпадут, предупреждения Ozon сохранятся. Принятие
          предупреждений может разрешить выполнение остальных изменений этой
          отправки, которые вы подтвердили ранее.
        </p>
        <label>
          Причина сверки
          <input
            value={reason}
            maxLength={1000}
            disabled={requested}
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
        <label className="catalog-check">
          <input
            type="checkbox"
            checked={acknowledge}
            disabled={requested}
            onChange={(event) => setAcknowledge(event.target.checked)}
          />
          Я принимаю предупреждения Ozon и разрешаю сверку с продолжением ранее
          подтверждённых изменений при совпадении выбранных полей
        </label>
        <button
          className="secondary"
          disabled={requested || !acknowledge || !reason.trim()}
          onClick={() => void reconcile()}
        >
          Сверить результат без повторной отправки
        </button>
        {message && <p role="status">{message}</p>}
      </div>
    </details>
  );
}

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

function WorkResult({
  work,
  scope,
  onRefresh,
}: {
  work: CatalogWork;
  scope: Scope;
  onRefresh: () => void;
}) {
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
          {work.state === "RECONCILED" && work.code === warningMatchCode
            ? warningMatchLabel
            : stateLabel(work.state)}
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
      {work.state === "RECONCILED" && work.code === warningMatchCode && (
        <p>
          Первоначальная отправка имела неизвестный результат. Сверка
          подтвердила выбранные поля; исходные предупреждения сохранены ниже.
        </p>
      )}
      {errors.map((issue, index) => (
        <ProviderIssue issue={issue} key={`${issue.field}:${index}`} />
      ))}
      {uncertain &&
        work.receipt.task_id &&
        work.command.target.account.provider === "ozon" && (
          <ReadOnlyReconciliation
            work={work}
            scope={scope}
            onRefresh={onRefresh}
          />
        )}
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
                <WorkResult
                  work={work}
                  scope={scope}
                  onRefresh={() => {
                    void query.refetch();
                  }}
                  key={work.id}
                />
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
