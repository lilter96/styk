import { useEffect, useId, useRef, useState } from "react";
import { Icon } from "./UI";
import { OrdersRecovery } from "./OrdersRecovery";
import { OrdersNativeSummary, nativeDocumentURL } from "./OrdersNativeSummary";
import {
  OrdersActionSummary,
  safeOrderArtifactURL,
} from "./OrdersActionSummary";
import { OrdersPositionPatch } from "./OrdersPositionPatch";
import { OrdersSplitEvidence } from "./OrdersSplitEvidence";
import { HistoryStatusIcon } from "./OrdersHistoryStatus";
import {
  historyActionTitle,
  historyInitiator,
  historyResult,
  historyService,
  historyTime,
  type HistoryRow,
} from "./OrdersHistoryPresentation";
import {
  orderDate,
  orderIssueLabel,
  orderOperationProblem,
  orderRequestError,
  type OrderScope,
  type OrderWork,
} from "./OrdersTypes";

type Props = {
  row: HistoryRow;
  scope: OrderScope;
  now: number;
  fetching: boolean;
  error?: unknown;
  onClose: () => void;
  onRefresh: () => void;
  onRecoveryAuthorization?: (
    posting: string,
    until: string | undefined,
  ) => void;
};
export function OrderHistoryDetailDrawer(props: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  useHistoryModal(dialog);
  const { row, onClose } = props;
  return (
    <dialog
      ref={dialog}
      className="history-drawer"
      aria-labelledby={titleID}
      aria-modal="true"
      onKeyDown={trapHistoryFocus}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="history-drawer-frame">
        <header className="history-drawer-top">
          <button type="button" className="history-back" onClick={onClose}>
            <Icon name="chevron-left" size="button" />
            История передач
          </button>
          <button
            type="button"
            className="history-close"
            aria-label="Закрыть подробности операции"
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        </header>
        <div className="history-drawer-body">
          <div className="history-detail-heading">
            <h2 id={titleID}>{historyActionTitle(row.work?.command.action)}</h2>
            <p>
              {row.work?.command.posting_number
                ? `Отправление ${row.work.command.posting_number}`
                : "Отправление не указано"}
            </p>
          </div>
          {!!props.error && (
            <p role="alert" className="orders-warning">
              {orderRequestError(props.error)} Показано последнее полученное
              состояние.
            </p>
          )}
          <HistoryExplanation row={row} />
          <HistoryTimeline row={row} />
          {row.work?.state === "UNKNOWN" && (
            <p className="history-info">
              <Icon name="error" />
              Повторять передачу сейчас не нужно — это может создать дубликат.
            </p>
          )}
          <HistoryActions {...props} />
          <HistoryFacts row={row} />
          <HistoryTechnicalDetails row={row} />
        </div>
        <footer className="history-drawer-footer">
          <button type="button" className="secondary" onClick={onClose}>
            Закрыть
          </button>
        </footer>
      </div>
    </dialog>
  );
}
function trapHistoryFocus(event: React.KeyboardEvent<HTMLDialogElement>) {
  if (event.key !== "Tab") return;
  const controls = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(
      "button, a[href], input, select, textarea, summary, [tabindex]",
    ),
  ).filter(
    (element) =>
      element.tabIndex >= 0 &&
      !element.matches(":disabled") &&
      element.getClientRects().length > 0,
  );
  const target = event.shiftKey ? controls.at(-1) : controls[0];
  const edge = event.shiftKey ? controls[0] : controls.at(-1);
  if (!target || document.activeElement !== edge) return;
  event.preventDefault();
  target.focus();
}
function useHistoryModal(ref: React.RefObject<HTMLDialogElement | null>) {
  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    if (element.showModal) element.showModal();
    else element.setAttribute("open", "");
    element.querySelector<HTMLButtonElement>("button")?.focus();
    return () => {
      element.close?.();
      document.body.style.overflow = overflow;
      if (previous?.isConnected) previous.focus();
    };
  }, [ref]);
}
function HistoryExplanation({ row }: { row: HistoryRow }) {
  const work = row.work;
  const result = historyResult(work?.state);
  const problem = orderOperationProblem(work?.code || work?.receipt?.code);
  return (
    <section
      className={`history-explanation ${result.tone}`}
      aria-label="Результат операции"
    >
      <HistoryStatusIcon tone={result.tone} />
      <div>
        <h3>{result.label}</h3>
        <p>{result.explanation}</p>
        {problem && <p>{problem}</p>}
        {work &&
          ["REJECTED", "BLOCKED"].includes(work.state) &&
          (work.code || work.receipt?.code) &&
          !problem && (
            <p>{orderIssueLabel(work.code || work.receipt?.code || "")}</p>
          )}
        {work?.receipt?.country_evidence && (
          <p>
            {work.receipt.country_evidence.requirement_resolved
              ? "Требование указать страну снято."
              : "Снятие требования указать страну не подтверждено."}{" "}
            Установленное значение страны отдельным чтением не подтверждено.
          </p>
        )}
      </div>
    </section>
  );
}
function HistoryTimeline({ row }: { row: HistoryRow }) {
  const result = historyResult(row.work?.state);
  const done = row.work?.state === "RECONCILED";
  const stopped = ["REJECTED", "BLOCKED"].includes(row.work?.state ?? "");
  return (
    <section className="history-detail-section">
      <h3>Что произошло</h3>
      <ol className="history-timeline">
        <li className="success">
          <HistoryStatusIcon tone="success" />
          <div>
            <strong>Поручение создано в STYK</strong>
            <time
              dateTime={row.operation.created_at}
              title={orderDate(row.operation.created_at)}
            >
              {historyTime(row.operation.created_at)}
            </time>
          </div>
        </li>
        <li className={result.tone}>
          <HistoryStatusIcon tone={result.tone} />
          <div>
            <strong>
              {done ? "Результат проверен в сервисе" : result.label}
            </strong>
            <small>
              {done
                ? "Подтверждено сверкой"
                : stopped
                  ? "Проверьте причину перед новым предпросмотром"
                  : "Подтверждения завершения пока нет"}
            </small>
          </div>
        </li>
        <li className={done ? "success" : "pending"}>
          <HistoryStatusIcon tone={done ? "success" : "pending"} />
          <div>
            <strong>{stopped ? "Действие остановлено" : "Завершение"}</strong>
            <small>
              {done
                ? "Действие выполнено"
                : stopped
                  ? result.label
                  : "Изменения не подтверждены"}
            </small>
          </div>
        </li>
      </ol>
    </section>
  );
}
function HistoryActions(props: Props) {
  const work = props.row.work;
  return (
    <section className="history-detail-section">
      <h3>Что можно сделать</h3>
      <div className="history-detail-actions">
        <button
          type="button"
          disabled={props.fetching}
          onClick={props.onRefresh}
          title="Прочитать сохранённое состояние в STYK"
        >
          <Icon name="refresh" size="button" />
          Обновить статус
        </button>
        <HistoryDocumentLinks work={work} now={props.now} />
      </div>
      <p className="history-caption">
        Обновляется сохранённое состояние в STYK. Новая передача не запускается.
      </p>
      {work?.state === "UNKNOWN" && (
        <OrdersRecovery
          scope={props.scope}
          work={work}
          onAuthorization={props.onRecoveryAuthorization}
        />
      )}
      {work &&
        ["REJECTED", "BLOCKED"].includes(work.state) &&
        work.command.native_movement && (
          <p className="history-caption">
            Варианты новой попытки доступны в действиях отправления. STYK
            повторно проверит результат прежней операции и условия передачи.
          </p>
        )}
    </section>
  );
}
const validMoyID = (id?: string) =>
  id && /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i.test(id)
    ? id
    : undefined;
function HistoryDocumentLinks({
  work,
  now,
}: {
  work?: OrderWork;
  now: number;
}) {
  if (!work) return null;
  const id = validMoyID(
    work.command.correlation?.customer_order_id ||
      (work.command.action.startsWith("mirror_")
        ? work.receipt?.external_id
        : undefined),
  );
  const document = nativeDocumentURL(
    work.command,
    work.native_document?.id ?? work.receipt?.external_id,
  );
  const artifact = work.receipt?.artifact;
  const url =
    work.state === "RECONCILED" &&
    artifact?.available &&
    (!artifact.expires_at || Date.parse(artifact.expires_at) > now)
      ? safeOrderArtifactURL(artifact.download_url)
      : undefined;
  return (
    <>
      {id && (
        <a
          className="history-document-link"
          href={`https://online.moysklad.ru/app/#customerorder/edit?id=${encodeURIComponent(id)}`}
          target="_blank"
          rel="noopener noreferrer"
        >
          <Icon name="arrow" size="button" />
          Открыть заказ МойСклад
        </a>
      )}
      {document && (
        <a
          className="history-document-link"
          href={document}
          target="_blank"
          rel="noopener noreferrer"
        >
          Открыть складской документ МойСклад
        </a>
      )}
      {url && (
        <a className="history-document-link" href={url}>
          Скачать этикетку
        </a>
      )}
      {work.state === "RECONCILED" && artifact?.expires_at && (
        <p className="history-artifact-note">
          {Date.parse(artifact.expires_at) > now
            ? `Этикетка доступна до ${orderDate(artifact.expires_at)}.`
            : "Срок хранения этикетки истёк."}
        </p>
      )}
    </>
  );
}
function HistoryFacts({ row }: { row: HistoryRow }) {
  const entries = [
    ["Дата", orderDate(row.operation.created_at)],
    ["Отправление", row.work?.command.posting_number || "Не указано"],
    ["Заказ", row.work?.command.commercial_order_id],
    ["Инициатор", historyInitiator(row.work)],
    ["Сервис", historyService(row.work)],
  ];
  return (
    <section className="history-detail-section">
      <h3>Сведения</h3>
      <dl className="history-facts">
        {entries
          .filter(([, value]) => value)
          .map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
      </dl>
    </section>
  );
}
function HistoryTechnicalDetails({ row }: { row: HistoryRow }) {
  const [open, setOpen] = useState(false);
  const work = row.work;
  return (
    <details
      className="history-technical"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>
        <Icon name="chevron-down" size="button" />
        <span>
          <strong>Технические сведения</strong>
          <small>Для поддержки и диагностики</small>
        </span>
      </summary>
      {open && (
        <div className="history-technical-body">
          <dl className="history-facts">
            <div>
              <dt>Operation ID</dt>
              <dd>{row.operation.id}</dd>
            </div>
            {work && (
              <>
                <div>
                  <dt>Work ID</dt>
                  <dd>{work.id}</dd>
                </div>
                <div>
                  <dt>Состояние</dt>
                  <dd>{work.state}</dd>
                </div>
                <div>
                  <dt>Код</dt>
                  <dd>{work.code || work.receipt?.code || "Не указан"}</dd>
                </div>
              </>
            )}
          </dl>
          {work?.command.position_patch && (
            <OrdersPositionPatch value={work.command.position_patch} />
          )}
          {work?.command.native_movement && (
            <OrdersNativeSummary command={work.command} historical />
          )}
          {work?.command.posting_action && (
            <OrdersActionSummary command={work.command} historical />
          )}
          {work &&
            ["posting_split", "posting_split_unassembled"].includes(
              work.command.action,
            ) && (
              <OrdersSplitEvidence receipt={work.receipt} state={work.state} />
            )}
          {work?.receipt?.task_ids?.map((task, index) => (
            <p key={`${task.id}:${index}`}>Задача обработки Ozon: {task.id}</p>
          ))}
          <details>
            <summary>Полные сведения, полученные от STYK</summary>
            <pre>{JSON.stringify(work ?? row.operation, null, 2)}</pre>
          </details>
        </div>
      )}
    </details>
  );
}
