import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  api,
  importAccount,
  sameImportScope,
  post,
  params,
  ApiError,
  type ImportOperation,
  type Batch,
  type ImportScope,
} from "./api";
import type { ImportSubmissionState } from "./ImportSubmission";
import { IntegrationDialog } from "./IntegrationDialog";

const phaseLabels: Record<string, string> = {
  READING_PRODUCTS: "Загружаем товары",
  READING_VARIANTS: "Загружаем варианты",
  READING_WAREHOUSES: "Загружаем склады",
  READING_STOCK: "Получаем остатки",
  READING_AGGREGATE_STOCK: "Получаем остатки",
  READING_WAREHOUSE_STOCK: "Получаем остатки по складам",
  PERSISTING: "Сохраняем данные",
};
const stateLabels: Record<ImportOperation["state"], string> = {
  QUEUED: "В очереди",
  RUNNING: "Обновляется",
  SUCCEEDED: "Успешно",
  FAILED: "Ошибка",
  CANCELLED: "Отменено",
  OUTCOME_UNKNOWN: "Результат уточняется",
};
const descriptions: Record<ImportOperation["state"], string> = {
  QUEUED: "Загрузка начнётся, когда освободится обработчик.",
  RUNNING: "Загружаем товары и остатки",
  SUCCEEDED: "Товары и остатки загружены",
  FAILED: "Не удалось загрузить данные. Предыдущие успешные загрузки доступны.",
  CANCELLED: "Загрузка отменена до начала обработки.",
  OUTCOME_UNKNOWN:
    "Проверяем, были ли данные сохранены. Не запускайте повторную загрузку до уточнения результата.",
};
const failures: Record<string, string> = {
  LIMIT_REACHED:
    "Достигнут лимит загрузки. Проверьте ограничения в подробностях.",
  API_FAILURE:
    "Сервис не ответил или вернул ошибку. Проверьте доступ в настройках.",
  INVALID_RESPONSE: "Не удалось проверить данные, полученные от сервиса.",
  PERSISTENCE_FAILURE: "Не удалось сохранить загруженные данные целиком.",
  CONFIGURATION_UNAVAILABLE:
    "Настройки выбранного аккаунта недоступны обработчику.",
  LEASE_EXHAUSTED: "Обработка прервалась; доступные попытки исчерпаны.",
  CANCELLED: "Загрузка остановлена до завершения.",
};
type History = { items: ImportOperation[]; has_next: boolean; offset: number };
const active = (state: string) =>
  ["QUEUED", "RUNNING", "OUTCOME_UNKNOWN"].includes(state);
const scopeQuery = (scope: ImportScope) =>
  params({ tenant: scope.tenant, ...importAccount(scope) });
const poll = (query: { state: { data?: ImportOperation; error: unknown } }) =>
  !query.state.error && query.state.data && active(query.state.data.state)
    ? 2000
    : false;
const pollHistory = (query: { state: { data?: History; error: unknown } }) =>
  !query.state.error && query.state.data?.items.some((op) => active(op.state))
    ? 2000
    : false;
function cardTimestamp(value: string) {
  const date = new Date(value),
    now = new Date();
  const time = date.toLocaleTimeString("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
  });
  return date.toDateString() === now.toDateString()
    ? `сегодня в ${time}`
    : date.toLocaleString("ru-RU", {
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
      });
}
function plural(value: number, one: string, few: string, many: string) {
  const n = value % 100;
  return n >= 11 && n <= 14
    ? many
    : value % 10 === 1
      ? one
      : value % 10 >= 2 && value % 10 <= 4
        ? few
        : many;
}
function timestamp(value: string | null) {
  return value
    ? new Date(value).toLocaleString("ru-RU", {
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
        year: "numeric",
      })
    : "—";
}
function duration(op: ImportOperation) {
  const total =
    op.started_at && op.finished_at
      ? Date.parse(op.finished_at) - Date.parse(op.started_at)
      : NaN;
  const hasTotal = Number.isFinite(total) && total >= 0;
  const milliseconds = hasTotal ? total : op.elapsed_ms;
  if (milliseconds <= 0) return null;
  const seconds = (milliseconds / 1000).toLocaleString("ru-RU", {
    maximumFractionDigits: 1,
  });
  return hasTotal ? `${seconds} с` : `${seconds} с за попытку`;
}

function Identifier({ label, value }: { label: string; value: string }) {
  const [message, setMessage] = useState("");
  return (
    <div className="technical-identifier">
      <dt>{label}</dt>
      <dd>
        <code>{value}</code>
        <button
          className="text-action"
          aria-label={`Скопировать: ${label}`}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value);
              setMessage("Скопировано");
            } catch {
              setMessage("Выделите и скопируйте идентификатор вручную");
            }
          }}
        >
          Копировать
        </button>
        {message && <span role="status">{message}</span>}
      </dd>
    </div>
  );
}
function TechnicalDetails({
  op,
  scope,
}: {
  op: ImportOperation;
  scope: ImportScope;
}) {
  const facts: [string, string | number | null][] = [
    ["Статус обработки", op.state],
    ["Этап обработки", op.phase],
    ["Результат обработки", op.outcome],
    ["Попытка", `${op.attempt} из ${op.max_attempts}`],
    ["Запросы", op.counts.requests],
    ["HTTP-попытки", op.counts.attempts],
    ["Попытки чтения страниц", op.counts.pages],
    ["Проверенные страницы", op.counts.completed_pages],
    ["Успешные JSON-ответы", op.counts.completed_requests],
    ["Создано", timestamp(op.created_at)],
    ["Начало", timestamp(op.started_at)],
    ["Завершение", timestamp(op.finished_at)],
    ["Длительность", duration(op)],
    ["Достигнутый лимит", op.limiting_resource],
  ];
  const limits: [string, number | undefined][] = [
    ["Запросы", op.limits.requests],
    ["HTTP-попытки", op.limits.attempts],
    ["Страницы", op.limits.pages],
    ["Товары", op.limits.products],
    ["Склады", op.limits.warehouses],
    ["Записи об остатках", op.limits.observations],
    ["Варианты", op.limits.variants],
    ["Записи по складам", op.limits.warehouse_observations],
    ["Размер ответа, байт", op.limits.response_bytes],
    ["Длительность, с", op.limits.duration_ns / 1e9],
    ["Транзакция базы данных, с", op.limits.database_timeout_ns / 1e9],
  ];
  return (
    <details className="technical-details">
      <summary>Технические детали</summary>
      <dl className="technical-identifiers">
        <Identifier label="Операция" value={op.id} />
        <Identifier label="Рабочее пространство" value={scope.tenant} />
        <Identifier label="Аккаунт" value={scope.account} />
        {op.batch_id !== null && (
          <Identifier label="Пакет данных" value={String(op.batch_id)} />
        )}
      </dl>
      <dl className="technical-facts">
        {facts.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value ?? "—"}</dd>
          </div>
        ))}
      </dl>
      <p>
        Обнаружено: товаров {op.counts.products}, складов {op.counts.warehouses}
        , записей об остатках {op.counts.observations}
        {op.counts.variants !== undefined &&
          `, вариантов ${op.counts.variants}`}
        {op.counts.warehouse_observations !== undefined &&
          `, записей по складам ${op.counts.warehouse_observations}`}
        . Счётчики относятся к текущей попытке обхода и сбрасываются при новой
        попытке. Они не означают, что данные сохранены.
      </p>
      {op.batch_created === false && (
        <p>
          Использован существующий идентичный пакет. Время наблюдения не
          изменилось.
        </p>
      )}
      <h4>Ограничения загрузки</h4>
      <dl className="technical-facts">
        {limits
          .filter(([, value]) => value !== undefined)
          .map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
      </dl>
    </details>
  );
}
function Confirm({
  title,
  children,
  onClose,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <IntegrationDialog title={title} onClose={onClose}>
      <div className="import-confirm">{children}</div>
    </IntegrationDialog>
  );
}

export function ImportPanel({
  scope,
  configured,
  submission,
  latestBatch,
  historyTarget,
  accountLabel,
  onStateChange,
  onSettings,
  onProbe,
  connectionProblem,
  repairCredentials,
  probeBusy,
  historyManaged = false,
}: {
  scope: ImportScope;
  configured: boolean;
  submission: ImportSubmissionState;
  latestBatch?: Batch;
  historyTarget?: HTMLElement | null;
  accountLabel?: string;
  onSettings?: () => void;
  onProbe?: () => void;
  connectionProblem?: string;
  repairCredentials?: boolean;
  probeBusy?: boolean;
  historyManaged?: boolean;
  onStateChange?: (
    scope: ImportScope,
    state: ImportOperation["state"] | undefined,
  ) => void;
}) {
  const client = useQueryClient();
  const name = scope.provider === "moysklad" ? "МойСклад" : "Ozon";
  const [offset, setOffset] = useState(0),
    [selected, setSelected] = useState(""),
    [confirm, setConfirm] = useState(false),
    [ack, setAck] = useState(false),
    [cancelTarget, setCancelTarget] = useState<ImportOperation | null>(null),
    [cancelBusy, setCancelBusy] = useState(false),
    [inspectionBusy, setInspectionBusy] = useState(false),
    [submitError, setSubmitError] = useState(""),
    [cancelMessage, setCancelMessage] = useState("");
  const cancelling = useRef(false),
    invalidated = useRef(new Set<string>());
  const queryString = scopeQuery(scope);
  const latest = useQuery({
    queryKey: [
      "import-history",
      scope.tenant,
      scope.provider,
      scope.account,
      0,
    ],
    queryFn: ({ signal }) =>
      api<History>(`/api/imports?${queryString}&limit=10&offset=0`, signal),
    retry: false,
    refetchInterval: pollHistory,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  const older = useQuery({
    queryKey: [
      "import-history",
      scope.tenant,
      scope.provider,
      scope.account,
      offset,
    ],
    queryFn: ({ signal }) =>
      api<History>(
        `/api/imports?${queryString}&limit=10&offset=${offset}`,
        signal,
      ),
    enabled: offset > 0,
    retry: false,
    refetchInterval: pollHistory,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  const history = offset === 0 ? latest : older;
  const latestID = latest.data?.items[0]?.id || "";
  const operationID = selected || latestID;
  const currentDetail = useQuery({
    queryKey: [
      "import-detail",
      scope.tenant,
      scope.provider,
      scope.account,
      latestID,
    ],
    queryFn: ({ signal }) =>
      api<ImportOperation>(`/api/imports/${latestID}?${queryString}`, signal),
    enabled: !!latestID,
    retry: false,
    refetchInterval: poll,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  const detail = useQuery({
    queryKey: [
      "import-detail",
      scope.tenant,
      scope.provider,
      scope.account,
      operationID,
    ],
    queryFn: ({ signal }) =>
      api<ImportOperation>(
        `/api/imports/${operationID}?${queryString}`,
        signal,
      ),
    enabled: !!selected,
    retry: false,
    refetchInterval: poll,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    const r = submission.result?.operation;
    if (
      r &&
      r.provider === scope.provider &&
      r.tenant === scope.tenant &&
      r.account === scope.account
    ) {
      setOffset(0);
    }
  }, [submission.result, scope.tenant, scope.provider, scope.account]);
  useEffect(() => {
    for (const error of [
      latest.error,
      history.error,
      detail.error,
      currentDetail.error,
    ])
      if (error instanceof ApiError && error.status === 401) {
        submission.markSessionLost();
        break;
      }
    // markSessionLost is invoked only on query errors; the hook object changes on render.
  }, [latest.error, history.error, detail.error, currentDetail.error]);
  useEffect(() => {
    for (const op of [currentDetail.data, detail.data]) {
      if (
        op?.state !== "SUCCEEDED" ||
        op.batch_id === null ||
        invalidated.current.has(op.id)
      )
        continue;
      invalidated.current.add(op.id);
      for (const key of [
        "integrations",
        "diagnostics",
        "products",
        "warehouses",
        "stock",
        "import-history",
      ])
        void client.invalidateQueries({ queryKey: [key] });
    }
  }, [currentDetail.data, detail.data, client]);
  const listed = latest.data?.items[0];
  // A terminal result in the newer history response must not be hidden by a
  // cached active detail response. Both describe the same scoped operation.
  const current =
    currentDetail.data && listed && currentDetail.data.id === listed.id
      ? listed && !active(listed.state) && active(currentDetail.data.state)
        ? listed
        : currentDetail.dataUpdatedAt >= latest.dataUpdatedAt
          ? currentDetail.data
          : listed
      : listed;
  const cancelOperation =
    cancelTarget?.id === current?.id
      ? current
      : cancelTarget?.id === detail.data?.id
        ? detail.data
        : cancelTarget;
  const submitted = submission.result?.operation;
  const submittedStillActive =
    submitted?.provider === scope.provider &&
    submitted?.tenant === scope.tenant &&
    submitted.account === scope.account &&
    active(submitted.state) &&
    !(current?.id === submitted.id && !active(current.state));
  const unsafe =
    !!submission.pending ||
    !!latest.error ||
    !latest.data ||
    !!submittedStillActive ||
    !!(current && active(current.state));
  const observedState = submittedStillActive ? submitted.state : current?.state;
  useEffect(() => {
    onStateChange?.(scope, observedState);
    // Scope identity is defined by these primitives, not the caller's object.
  }, [
    scope.tenant,
    scope.provider,
    scope.account,
    observedState,
    onStateChange,
  ]);
  const samePending =
    !!submission.pending &&
    sameImportScope(submission.pending.intent.scope, scope);
  async function cancelQueued() {
    if (
      cancelling.current ||
      !cancelOperation ||
      cancelOperation.state !== "QUEUED"
    )
      return;
    cancelling.current = true;
    setCancelBusy(true);
    setCancelMessage("");
    try {
      await post(
        `/api/imports/${cancelOperation.id}/cancel`,
        {
          tenant: scope.tenant,
          ...importAccount(scope),
          acknowledge: true,
        },
        AbortSignal.timeout(30000),
      );
      setCancelTarget(null);
      setCancelMessage("Загрузка отменена.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        submission.markSessionLost();
        setCancelMessage(
          "Сессия завершилась. Войдите снова и проверьте результат отмены.",
        );
      } else if (
        error instanceof ApiError &&
        error.code === "CANCELLATION_UNAVAILABLE"
      )
        setCancelMessage(
          "Обработка уже началась или завершилась. Отменить можно только загрузку в очереди.",
        );
      else
        setCancelMessage(
          "Результат отмены неизвестен. Обновите статус перед следующим действием.",
        );
    } finally {
      cancelling.current = false;
      setCancelBusy(false);
      void detail.refetch();
      void currentDetail.refetch();
      void latest.refetch();
      if (offset > 0) void older.refetch();
    }
  }
  const completed = !!(latestBatch?.present && latestBatch.completed);
  function historyDetails(op: ImportOperation) {
    const item =
      selected === op.id && detail.data?.id === op.id ? detail.data : op;
    return (
      <div className="history-entry-detail">
        {selected === op.id && detail.isLoading && (
          <p role="status">Уточняем результат…</p>
        )}
        {selected === op.id && !!detail.error && (
          <div role="alert">
            Статус загрузки недоступен.{" "}
            <button
              className="text-action"
              onClick={() => void detail.refetch()}
            >
              Повторить проверку
            </button>
          </div>
        )}
        <p>{descriptions[item.state]}</p>
        {item.state === "FAILED" && (
          <p role="alert">
            {failures[item.outcome || ""] ||
              "Проверьте подключение и подробности ошибки перед новой попыткой."}
          </p>
        )}
        {item.state === "SUCCEEDED" && (
          <>
            <p className="history-counts">
              Обнаружено: товаров {item.counts.products}, складов{" "}
              {item.counts.warehouses}
              {item.counts.variants !== undefined &&
                `, вариантов ${item.counts.variants}`}
              .
            </p>
            <p>
              Счётчики описывают этот обход. Товары и склады показывают
              последнюю доступную загрузку.
            </p>
          </>
        )}
        <nav
          className="history-links"
          aria-label={`${name}: результаты загрузки`}
        >
          {item.state === "SUCCEEDED" && (
            <>
              <Link to="/products">Товары</Link>
              <Link to="/warehouses">Склады</Link>
            </>
          )}
          <Link to="/diagnostics">Диагностика</Link>
        </nav>
        {item.state === "QUEUED" && (
          <button className="secondary" onClick={() => setCancelTarget(item)}>
            Отменить загрузку
          </button>
        )}
        <TechnicalDetails op={item} scope={scope} />
      </div>
    );
  }
  const historyView = (
    <details
      className="provider-history"
      aria-label={`${name}: история обновлений`}
    >
      <summary>
        <span>{name}</span>
        <span className="history-preview">
          {latest.error
            ? "История недоступна"
            : current
              ? stateLabels[current.state]
              : latest.isLoading
                ? "Загружаем историю…"
                : "Загрузок пока нет"}
          {current && (
            <time dateTime={current.created_at}>
              {timestamp(current.created_at)}
            </time>
          )}
        </span>
        {current && (
          <span className="history-latest-summary">
            {current.state === "FAILED"
              ? "Ошибка загрузки"
              : current.state === "OUTCOME_UNKNOWN"
                ? "Проверяем результат загрузки"
                : current.state === "RUNNING"
                  ? phaseLabels[current.phase] || "Загружаем данные"
                  : descriptions[current.state]}
          </span>
        )}
      </summary>
      <div className="provider-history-content">
        {history.isLoading && <p role="status">Загружаем историю…</p>}
        {!!history.error && (
          <div role="alert">
            Не удалось загрузить историю.{" "}
            <button
              className="text-action"
              onClick={() => void history.refetch()}
            >
              Повторить
            </button>
          </div>
        )}
        {history.data?.items.length === 0 && (
          <p>На этой странице загрузок нет.</p>
        )}
        <div className="history-entries">
          {history.data?.items.map((op) => (
            <details
              className="history-entry"
              key={op.id}
              onToggle={(e) => {
                if (e.currentTarget.open) setSelected(op.id);
              }}
            >
              <summary>
                <span
                  className={`history-result state-${op.state.toLowerCase()}`}
                >
                  {stateLabels[op.state]}
                </span>
                <time dateTime={op.created_at}>{timestamp(op.created_at)}</time>
                {duration(op) && (
                  <span className="history-duration">{duration(op)}</span>
                )}
                <span className="history-summary">
                  {op.state === "SUCCEEDED"
                    ? "Товары и остатки загружены"
                    : descriptions[op.state]}
                </span>
                <span className="history-more">Подробнее</span>
              </summary>
              {historyDetails(op)}
            </details>
          ))}
        </div>
        <div className="pagination">
          <button
            className="secondary"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - 10))}
          >
            Предыдущие
          </button>
          <button
            className="secondary"
            disabled={!history.data?.has_next || offset >= 10000}
            onClick={() => setOffset(offset + 10)}
          >
            Следующие
          </button>
        </div>
        {offset >= 10000 && history.data?.has_next && (
          <p>Достигнут предел просмотра истории.</p>
        )}
      </div>
    </details>
  );
  return (
    <section
      className="import-panel"
      data-provider={scope.provider}
      aria-label={`${name}: импорт данных`}
    >
      {connectionProblem ? (
        <p className="provider-summary">{connectionProblem}</p>
      ) : (
        !(current && active(current.state)) &&
        current?.state !== "FAILED" && (
          <p className="import-baseline">
            {completed && latestBatch.observed_at ? (
              <>
                Данные обновлены{" "}
                <time dateTime={latestBatch.observed_at}>
                  {cardTimestamp(latestBatch.observed_at)}
                </time>
                .
              </>
            ) : (
              "Каталог ещё не загружен. Загрузите товары, склады и остатки."
            )}
          </p>
        )
      )}
      {latest.isLoading && <p role="status">Проверяем состояние загрузки…</p>}
      {!!latest.error && (
        <div className="import-warning" role="alert">
          История недоступна. Перед загрузкой нужно проверить предыдущие
          запросы.{" "}
          <button className="text-action" onClick={() => void latest.refetch()}>
            Повторить проверку
          </button>
        </div>
      )}
      {!!currentDetail.error && (
        <div className="import-warning" role="alert">
          Не удалось уточнить статус последней загрузки.{" "}
          <button
            className="text-action"
            onClick={() => void currentDetail.refetch()}
          >
            Обновить статус
          </button>
        </div>
      )}
      {current && active(current.state) && (
        <div
          className={`import-progress state-${current.state.toLowerCase()}`}
          role={current.state === "OUTCOME_UNKNOWN" ? "alert" : "status"}
        >
          <strong>
            {current.state === "RUNNING"
              ? `Обновляем данные ${name}`
              : stateLabels[current.state]}
          </strong>
          <p>
            {current.state === "RUNNING"
              ? `${phaseLabels[current.phase] || descriptions.RUNNING}…`
              : descriptions[current.state]}
          </p>
          {current.state === "RUNNING" && (
            <div
              className="indeterminate-bar"
              role="progressbar"
              aria-label={`Загрузка ${name}`}
            />
          )}
          {current.state === "RUNNING" && current.counts.products > 0 && (
            <p>
              Обнаружено товаров:{" "}
              {current.counts.products.toLocaleString("ru-RU")}. Данные текущей
              попытки, ещё не сохранены.
            </p>
          )}
          {current.state === "QUEUED" && (
            <button
              className="text-action"
              disabled={cancelBusy}
              onClick={() => setCancelTarget(current)}
            >
              Отменить загрузку
            </button>
          )}
        </div>
      )}
      {current?.state === "FAILED" && (
        <div className="import-warning" role="alert">
          <strong>Не удалось обновить данные</strong>
          <p>
            {failures[current.outcome || ""] ||
              "Загрузка завершилась с ошибкой. Проверьте подробности и подключение."}
          </p>
          <p>Предыдущие успешные загрузки доступны.</p>
        </div>
      )}
      {current?.state === "CANCELLED" && (
        <p className="import-caption">Загрузка отменена до начала обработки.</p>
      )}
      {!configured && (
        <p className="import-warning">
          Для загрузки выберите аккаунт, настроенный на сервере. История
          выбранного аккаунта доступна.
        </p>
      )}
      {submitError && <p role="alert">{submitError}</p>}
      <div className="provider-actions">
        {repairCredentials && onSettings ? (
          <>
            <button onClick={onSettings}>Обновить ключ</button>
            {onProbe && (
              <button
                className="text-action"
                disabled={probeBusy}
                onClick={onProbe}
              >
                {probeBusy ? "Проверяем…" : "Проверить снова"}
              </button>
            )}
          </>
        ) : (
          <>
            <button
              className={`import-primary ${completed ? "" : "first-import"}`}
              disabled={!configured || unsafe}
              onClick={() => {
                setAck(false);
                setSubmitError("");
                setConfirm(true);
              }}
            >
              {completed ? "Обновить данные" : "Загрузить данные"}
            </button>
            {onSettings && (
              <button className="text-action" onClick={onSettings}>
                Настройки
              </button>
            )}
          </>
        )}
      </div>
      <div className="provider-footnote">
        {connectionProblem && completed && latestBatch.observed_at ? (
          <>
            Данные от{" "}
            <time dateTime={latestBatch.observed_at}>
              {timestamp(latestBatch.observed_at)}
            </time>{" "}
            — могут быть устаревшими
          </>
        ) : (
          <>
            Мы только читаем данные
            {scope.provider === "ozon"
              ? " — загрузка не меняет остатки в Ozon"
              : ""}
          </>
        )}
        {completed && latestBatch.counts && (
          <dl className="catalog-counts">
            {latestBatch.counts.products !== undefined && (
              <div>
                <dt>
                  {plural(
                    latestBatch.counts.products,
                    "товар",
                    "товара",
                    "товаров",
                  )}
                </dt>
                <dd>{latestBatch.counts.products.toLocaleString("ru-RU")}</dd>
              </div>
            )}
            {scope.provider === "moysklad" &&
              latestBatch.counts.variants !== undefined && (
                <div>
                  <dt>
                    {plural(
                      latestBatch.counts.variants,
                      "вариант",
                      "варианта",
                      "вариантов",
                    )}
                  </dt>
                  <dd>{latestBatch.counts.variants.toLocaleString("ru-RU")}</dd>
                </div>
              )}
            {latestBatch.counts.warehouses !== undefined && (
              <div>
                <dt>
                  {plural(
                    latestBatch.counts.warehouses,
                    "склад",
                    "склада",
                    "складов",
                  )}
                </dt>
                <dd>{latestBatch.counts.warehouses.toLocaleString("ru-RU")}</dd>
              </div>
            )}
          </dl>
        )}
      </div>
      {confirm && (
        <Confirm
          title={`Загрузить данные ${name}?`}
          onClose={() => setConfirm(false)}
        >
          <p>
            Загрузим товары{scope.provider === "moysklad" ? ", варианты" : ""},
            склады и остатки
            {accountLabel ? `: ${accountLabel}` : " выбранного аккаунта"}.
            Данные в сервисе не изменятся.
          </p>
          <p>
            Загрузка не создаёт соответствия и не включает публикацию остатков.
            Данные получаются последовательно и могут меняться во время
            загрузки.
          </p>
          <details className="technical-details">
            <summary>Технические детали</summary>
            <dl>
              <Identifier label="Рабочее пространство" value={scope.tenant} />
              <Identifier label="Аккаунт" value={scope.account} />
            </dl>
            <p>Действуют установленные ограничения загрузки.</p>
          </details>
          <label>
            <input
              type="checkbox"
              checked={ack}
              onChange={(e) => setAck(e.target.checked)}
            />
            Подтверждаю загрузку данных выбранного аккаунта
          </label>
          <button
            disabled={!ack || unsafe || !configured}
            onClick={() => {
              try {
                submission.start(scope);
                setConfirm(false);
              } catch {
                setSubmitError(
                  "Браузер не может создать защищённый запрос. Загрузка не началась.",
                );
              }
            }}
          >
            Подтвердить загрузку
          </button>
        </Confirm>
      )}
      {samePending &&
        (submission.sessionLost ||
          submission.message.startsWith("IDEMPOTENCY_CONFLICT")) && (
          <button
            className="secondary"
            disabled={inspectionBusy}
            onClick={async () => {
              setInspectionBusy(true);
              const refreshed = await latest.refetch();
              setInspectionBusy(false);
              if (!refreshed.error && refreshed.data) {
                setOffset(0);
                submission.inspected(scope);
              }
            }}
          >
            {inspectionBusy
              ? "Проверяем историю…"
              : "Обновить и проверить историю"}
          </button>
        )}
      {cancelMessage && <p role="alert">{cancelMessage}</p>}
      {cancelTarget && (
        <Confirm
          title="Отменить загрузку?"
          onClose={() => setCancelTarget(null)}
        >
          <p>
            Можно отменить только загрузку в очереди. Уже сохранённые данные
            останутся доступны.
          </p>
          <button
            disabled={cancelBusy || cancelOperation?.state !== "QUEUED"}
            onClick={() => void cancelQueued()}
          >
            {cancelBusy ? "Отменяем…" : "Подтвердить отмену"}
          </button>
        </Confirm>
      )}
      {historyTarget
        ? createPortal(historyView, historyTarget)
        : historyManaged
          ? null
          : historyView}
    </section>
  );
}
