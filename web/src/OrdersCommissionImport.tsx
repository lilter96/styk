import { OrdersFinancialLedger } from "./OrdersFinancialLedger";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, post } from "./api";
import {
  commissionDocumentURL,
  commissionStateLabel,
  type CommissionPlan,
  type CommissionPreview,
  type CommissionWork,
} from "./OrdersFinanceTypes";
import {
  orderDate,
  orderFieldText,
  orderMoney,
  orderRequestError,
  orderScopeBody,
  ordersURL,
  sameOrderScope,
  type OrderScope,
  type OrdersConfigData,
} from "./OrdersTypes";

type Pending = { preview_id: string; period: string; idempotency_key: string };
const storageKey = (scope: OrderScope) =>
  `styk-commission-pending:${JSON.stringify(orderScopeBody(scope))}`;
function clearPendingStorage(scope: OrderScope): boolean {
  try {
    sessionStorage.removeItem(storageKey(scope));
    return true;
  } catch {
    return false;
  }
}
function loadPending(scope: OrderScope): Pending | null {
  try {
    const value: unknown = JSON.parse(
      sessionStorage.getItem(storageKey(scope)) ?? "null",
    );
    if (
      !value ||
      typeof value !== "object" ||
      !("preview_id" in value) ||
      !("period" in value) ||
      !("idempotency_key" in value) ||
      typeof value.preview_id !== "string" ||
      typeof value.period !== "string" ||
      typeof value.idempotency_key !== "string"
    )
      return null;
    return value as Pending;
  } catch {
    return null;
  }
}
function validWork(work: CommissionWork, scope: OrderScope) {
  return (
    !!work?.id &&
    sameOrderScope(work.scope, scope) &&
    sameOrderScope(work.plan?.scope, scope) &&
    !!work.plan?.source?.period
  );
}
function issueLabel(code: string) {
  const exact: Record<string, string> = {
    finance_policy_not_enabled:
      "Финансовое разрешение выключено. Включите его после выбора договора, валюты, цены и НДС и сохраните настройки. Диагностическая проверка не создаёт документ.",
    finance_policy_or_references_unverified:
      "Не подтверждены бухгалтерские условия: действующий комиссионный договор, организация и контрагент, валюта и курс, основание цены, НДС и правила дополнительных компонентов. Заполните блок «Черновики отчётов комиссионера» выше. Подходящий договор должен быть доступен в МойСклад; STYK его не создаёт автоматически.",
    FINANCE_DOCUMENT_POSTED_EXTERNALLY:
      "При последней независимой сверке документ был проведён вне STYK. STYK не изменяет и не перепроводит его; бухгалтер проверяет проведённый документ и исходный отчёт вручную.",
    FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW:
      "В МойСклад уже есть отчёт с маркером предыдущей попытки. Сверьте документ и историю попыток вручную; создание нового черновика за этот период заблокировано.",
    FINANCE_EXISTING_DOCUMENT_UNVERIFIED:
      "Наличие предыдущего отчёта МойСклад не удалось проверить. Нужна ручная сверка документа и истории попыток; создание нового черновика за этот период заблокировано.",
    finance_report_period_unverified:
      "Период не подтверждён заголовком отчёта Ozon. Сверьте закрытый период с исходным документом.",
    finance_source_incomplete_or_stale:
      "Исходный отчёт неполный или устарел. Получите свежие данные и повторите проверку.",
    finance_rounding_requires_review:
      "Для сумм требуется отдельное решение об округлении. Проверьте строки с бухгалтером; STYK не округляет их молча.",
    finance_legacy_or_open_period_unsupported:
      "Этот период не поддерживается для финансового черновика. Выберите поддерживаемый закрытый месяц.",
  };
  if (exact[code]) return exact[code];
  if (/return/i.test(code))
    return "Финансовые возвраты не подтверждены текущими данными или политикой. Проверьте их состав и выбранную бухгалтерскую обработку возвратов.";
  if (/component|compensation|coinvestment|stars/i.test(code))
    return "Дополнительные компоненты отчёта неизвестны или имеют неподдерживаемые ненулевые значения. Проверьте полный финансовый документ вручную.";
  if (/mapping|product/i.test(code))
    return "Не подтверждены точные связи товаров. Проверьте сопоставления и выполните новую проверку отчёта.";
  if (/currency|rate/i.test(code))
    return "Проверьте валюту отчёта, договор и явно заданный курс.";
  if (/price|total|amount|reward/i.test(code))
    return "Суммы или выбранное основание цены не подтверждены. Сверьте исходный отчёт с бухгалтером.";
  if (/contract|reference/i.test(code))
    return "Проверьте организацию, контрагента и комиссионный договор МойСклад.";
  if (/changed|correct/i.test(code))
    return "Исходный отчёт или настройки изменились. Существующий документ требует ручной проверки; автоматического перезаписывания нет.";
  return "Условия финансового черновика не подтверждены. Проверьте исходный отчёт, бухгалтерские настройки и связи товаров.";
}

function CommissionWorkResult({ work }: { work: CommissionWork }) {
  const url = commissionDocumentURL(work);
  return (
    <article className="settings-commission-work">
      <h4>
        {work.plan.source.period.year}-
        {String(work.plan.source.period.month).padStart(2, "0")} ·{" "}
        {work.plan.source.number || "Отчёт Ozon"}
      </h4>
      <p>
        <strong>{commissionStateLabel(work)}</strong>
      </p>
      <p>
        {work.code === "FINANCE_DOCUMENT_POSTED_EXTERNALLY"
          ? issueLabel(work.code)
          : work.state === "RECONCILED"
            ? work.plan.accounting?.complete === false
              ? "Поддержанная часть сохранена в непроведённом документе. Финансовая сверка не завершена: остаток требует бухгалтера; выплата и складское движение не подтверждены."
              : "Документ непроведённый. Бухгалтер проверяет его отдельно; выплата и складское движение не подтверждены."
            : work.state === "UNKNOWN"
              ? "Повторная запись заблокирована. Обновите историю: STYK отдельно сверяет документ и показывает сохранённый результат."
              : "Фактический результат определяется отдельной сверкой МойСклад."}
      </p>
      {work.source_changed && (
        <p className="settings-warning">
          Исходный отчёт изменился. Сравните его с существующим документом
          вручную; черновик не перезаписывается.
        </p>
      )}
      {(work.state === "BLOCKED" || work.state === "REJECTED") && (
        <p>{issueLabel(work.code ?? "")}</p>
      )}
      {url && (
        <a href={url} target="_blank" rel="noopener noreferrer">
          {work.code === "FINANCE_DOCUMENT_POSTED_EXTERNALLY"
            ? "Открыть проведённый отчёт МойСклад"
            : "Открыть непроведённый отчёт МойСклад"}
        </a>
      )}
      {work.plan.accounting && (
        <details>
          <summary>Состав сохранённого финансового черновика</summary>
          <OrdersFinancialLedger ledger={work.plan.accounting} planned />
        </details>
      )}
      <p>Обновлено: {orderDate(work.updated_at)}.</p>
    </article>
  );
}

function CommissionPlanSummary({ plan }: { plan: CommissionPlan }) {
  return (
    <>
      <h4>
        Непроведённый черновик за {plan.source.period.year}-
        {String(plan.source.period.month).padStart(2, "0")}
      </h4>
      <dl className="settings-realization-facts settings-commission-facts">
        <div>
          <dt>Исходный отчёт</dt>
          <dd>{plan.source.number || "Номер не предоставлен"}</dd>
        </div>
        <div>
          <dt>Строк</dt>
          <dd>{plan.lines.length}</dd>
        </div>
        <div>
          <dt>
            {plan.returns?.length
              ? "Сумма черновика с учётом возвратов"
              : "Сумма реализации"}
          </dt>
          <dd>
            {plan.totals.gross} {plan.totals.currency}
          </dd>
        </div>
        <div>
          <dt>Вознаграждение</dt>
          <dd>
            {plan.totals.reward} {plan.totals.currency}
          </dd>
        </div>
        <div>
          <dt>Сумма после вознаграждения</dt>
          <dd>
            {plan.totals.net} {plan.totals.currency}
          </dd>
        </div>
        <div>
          <dt>Цена</dt>
          <dd>
            {plan.policy.price_basis === "SELLER_PRICE"
              ? "Цена продавца из отчёта"
              : plan.policy.price_basis === "REALIZED_PRICE"
                ? "Цена реализации из отчёта"
                : "Основание цены не определено"}
          </dd>
        </div>
        <div>
          <dt>НДС</dt>
          <dd>
            {plan.policy.vat}% · учёт:{" "}
            {orderFieldText(plan.policy.vat_enabled, (value) =>
              value ? "да" : "нет",
            )}{" "}
            · включён:{" "}
            {orderFieldText(plan.policy.vat_included, (value) =>
              value ? "да" : "нет",
            )}
          </dd>
        </div>
        <div>
          <dt>Курс</dt>
          <dd>{plan.policy.rate_value}</dd>
        </div>
      </dl>
      <p>
        Это суммы подготовленного черновика, а не подтверждение выплаты или
        полноты исходной отчётности Ozon. Сверьте все позиции и настройки с
        бухгалтером.
      </p>
      <details>
        <summary>Договор, ссылки и позиции черновика</summary>
        <p>
          Организация: {plan.policy.organization_id}. Контрагент:{" "}
          {plan.policy.agent_id}. Договор: {plan.policy.contract_id}. Валюта
          МойСклад: {plan.policy.currency_id}.
        </p>
        <ul className="settings-realization-rows">
          {plan.lines.map((line) => (
            <li key={line.source_row}>
              <strong>Строка источника {line.source_row}</strong>
              <span>
                Товар МойСклад: {line.mapping.moysklad_kind}{" "}
                {line.mapping.moysklad_id}
              </span>
              <span>
                Количество: {line.quantity}; цена: {orderMoney(line.price)};
                вознаграждение: {orderMoney(line.reward)}.
              </span>
            </li>
          ))}
        </ul>
      </details>
      {!!plan.returns?.length && (
        <>
          <h4>Финансовые возвраты в черновике: {plan.returns.length}</h4>
          <p>
            Сумма возвратов: {plan.return_totals?.gross}{" "}
            {plan.return_totals?.currency}; вознаграждение:{" "}
            {plan.return_totals?.reward}; после вознаграждения:{" "}
            {plan.return_totals?.net}. Это позиции непроведённого отчёта, без
            складского движения.
          </p>
          <details>
            <summary>Позиции финансовых возвратов</summary>
            <ul className="settings-realization-rows">
              {plan.returns.map((line) => (
                <li key={line.source_row}>
                  <strong>Строка источника {line.source_row}</strong>
                  <span>
                    Товар МойСклад: {line.mapping.moysklad_kind}{" "}
                    {line.mapping.moysklad_id}
                  </span>
                  <span>
                    Количество: {line.quantity}; цена: {orderMoney(line.price)};
                    вознаграждение: {orderMoney(line.reward)}.
                  </span>
                </li>
              ))}
            </ul>
          </details>
        </>
      )}
      <p>Проверка действует до {orderDate(plan.expires_at)}.</p>
    </>
  );
}

function CommissionSourceDiff({
  diff,
}: {
  diff: NonNullable<CommissionPreview["source_diff"]>;
}) {
  const [page, setPage] = useState(0);
  const changes = diff.component_changes ?? [];
  const value = (field: import("./OrdersTypes").OrderField<string>) =>
    field.presence === "missing"
      ? "Поле отсутствует"
      : field.presence === "null"
        ? "NULL — значение неизвестно"
        : orderFieldText(field);
  const rows = (values: number[] | null) => {
    const all = values ?? [];
    return all.length
      ? all.slice(0, 25).join(", ") +
          (all.length > 25 ? `; ещё ${all.length - 25}` : "")
      : "нет";
  };
  const fingerprint = (value: string) =>
    value.length > 12 ? `${value.slice(0, 12)}…` : value;
  return (
    <div className="settings-commission-source-diff">
      <h4>Изменения исходного отчёта</h4>
      <p>
        Сохранённый источник:{" "}
        <code title={diff.before_fingerprint}>
          {fingerprint(diff.before_fingerprint)}
        </code>
        . Новый источник:{" "}
        <code title={diff.after_fingerprint}>
          {fingerprint(diff.after_fingerprint)}
        </code>
        .
      </p>
      <p>
        {diff.header_changed
          ? "Заголовок отчёта изменён."
          : "Заголовок отчёта без изменений."}
      </p>
      <p>Добавлены строки: {rows(diff.added_rows)}</p>
      <p>Удалены строки: {rows(diff.removed_rows)}</p>
      <p>Изменены строки: {rows(diff.changed_rows)}</p>
      {changes.length > 0 && (
        <details>
          <summary>Изменения финансовых компонентов ({changes.length})</summary>
          <ul className="settings-realization-rows">
            {changes.slice(page * 25, (page + 1) * 25).map((item, index) => (
              <li
                key={`${item.source_row}:${item.side}:${item.component}:${index}`}
              >
                <strong>
                  Строка {item.source_row} ·{" "}
                  {item.side === "SALE"
                    ? "Продажа"
                    : item.side === "RETURN"
                      ? "Финансовый возврат"
                      : item.side}{" "}
                  · {item.component}
                </strong>
                <span>
                  {item.kind === "ADDED"
                    ? "Добавлен"
                    : item.kind === "REMOVED"
                      ? "Удалён"
                      : "Изменён"}
                  : {value(item.before)} → {value(item.after)}.
                </span>
                {(item.before_digest || item.after_digest) && (
                  <span>
                    Изменение содержимого зафиксировано по контрольным суммам;
                    непрочитанные значения не приравниваются к нулю.
                  </span>
                )}
              </li>
            ))}
          </ul>
          <div
            className="settings-pagination"
            aria-label="Страницы изменений компонентов"
          >
            <button
              type="button"
              className="secondary"
              disabled={page === 0}
              onClick={() => setPage(page - 1)}
            >
              Предыдущие изменения
            </button>
            <span>
              {page * 25 + 1}–{Math.min((page + 1) * 25, changes.length)} из{" "}
              {changes.length}
            </span>
            <button
              type="button"
              className="secondary"
              disabled={(page + 1) * 25 >= changes.length}
              onClick={() => setPage(page + 1)}
            >
              Следующие изменения
            </button>
          </div>
        </details>
      )}
      <p>
        Показаны первые 25 номеров каждой группы. Сверьте корректировку с
        бухгалтером; существующий документ автоматически не изменяется.
      </p>
    </div>
  );
}

export function OrdersCommissionImport({
  scope,
  config,
}: {
  scope: OrderScope;
  config: OrdersConfigData;
}) {
  const previous = new Date();
  previous.setUTCDate(1);
  previous.setUTCMonth(previous.getUTCMonth() - 1);
  const maximum = `${previous.getUTCFullYear()}-${String(previous.getUTCMonth() + 1).padStart(2, "0")}`;
  const [pending, setPending] = useState<Pending | null>(() =>
    loadPending(scope),
  );
  const [period, setPeriod] = useState(pending?.period ?? maximum);
  const [preview, setPreview] = useState<CommissionPreview | null>(null);
  const [result, setResult] = useState<CommissionWork | null>(null);
  const [consent, setConsent] = useState(false);
  const [retryConsent, setRetryConsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());
  const lock = useRef(false);
  const active = config.finance?.enabled === true;
  const history = useQuery({
    queryKey: ["orders-commission-operations", scope],
    enabled: !!config.finance || !!pending,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: config.finance || pending ? 15000 : false,
    queryFn: async ({ signal }) => {
      const value = await api<{ operations: CommissionWork[] }>(
        ordersURL("finance/operations", scope, { limit: "25" }),
        signal,
      );
      if (
        !Array.isArray(value.operations) ||
        value.operations.some((work) => !validWork(work, scope))
      )
        throw new Error("commission history scope mismatch");
      return value.operations;
    },
  });
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    if (!pending) return;
    const found = history.data?.find(
      (work) => work.preview_id === pending.preview_id,
    );
    if (!found) return;
    const cleared = clearPendingStorage(scope);
    setPending(null);
    setResult(found);
    setError(
      cleared
        ? ""
        : "Поручение найдено на сервере, но локальная запись подтверждения недоступна. Проверьте настройки хранения данных браузера.",
    );
  }, [history.data, pending, scope]);
  const plan = preview?.plan;
  const existing = preview?.existing_work;
  const changed = preview?.source_changed || existing?.source_changed;
  const retry =
    !!existing &&
    ["BLOCKED", "REJECTED"].includes(existing.state) &&
    existing.dispatch_count === 0 &&
    ![
      "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW",
      "FINANCE_EXISTING_DOCUMENT_UNVERIFIED",
    ].includes(existing.code ?? "") &&
    !changed;
  const expired =
    !plan?.expires_at ||
    !Number.isFinite(Date.parse(plan.expires_at)) ||
    Date.parse(plan.expires_at) <= now;
  const ready =
    active &&
    !!plan?.id &&
    plan.config_revision === config.revision &&
    preview?.validation.length === 0 &&
    !expired &&
    !changed &&
    (!existing || retry);
  const validPeriod =
    /^\d{4}-\d{2}$/.test(period) && period >= "2023-08" && period <= maximum;
  async function check() {
    if (lock.current || pending || !validPeriod) return;
    lock.current = true;
    setBusy(true);
    setError("");
    setConsent(false);
    setRetryConsent(false);
    setPreview(null);
    setResult(null);
    try {
      const [year, month] = period.split("-").map(Number);
      const value = await post<CommissionPreview>(
        "/api/orders/finance/preview",
        {
          ...orderScopeBody(scope),
          expected_revision: config.revision,
          period: { year, month },
        },
        AbortSignal.timeout(60000),
      );
      if (
        !Array.isArray(value.validation) ||
        (value.plan?.id &&
          (!sameOrderScope(value.plan.scope, scope) ||
            value.plan.config_revision !== config.revision ||
            value.plan.source.period.year !== year ||
            value.plan.source.period.month !== month)) ||
        (value.existing_work && !validWork(value.existing_work, scope))
      )
        throw new Error("commission preview scope mismatch");
      setPreview(value);
    } catch (cause) {
      setError(orderRequestError(cause));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function approve() {
    if (
      lock.current ||
      pending ||
      !ready ||
      !consent ||
      (retry && !retryConsent) ||
      !plan
    )
      return;
    const previewID = preview?.id ?? plan.id;
    const intent = {
      preview_id: previewID,
      period,
      idempotency_key: crypto.randomUUID(),
    };
    try {
      sessionStorage.setItem(storageKey(scope), JSON.stringify(intent));
    } catch {
      setError(
        "Не удалось сохранить идентификатор подтверждения в этой вкладке. Разрешите хранение данных сайта перед созданием черновика.",
      );
      return;
    }
    lock.current = true;
    setBusy(true);
    setError("");
    setPending(intent);
    try {
      const work = await post<CommissionWork>(
        "/api/orders/finance/approve",
        {
          ...orderScopeBody(scope),
          preview_id: previewID,
          idempotency_key: intent.idempotency_key,
          acknowledge: true,
          ...(retry ? { retry_work_id: existing?.id } : {}),
        },
        AbortSignal.timeout(60000),
      );
      if (!validWork(work, scope) || work.preview_id !== previewID)
        throw new Error("commission approval scope mismatch");
      const cleared = clearPendingStorage(scope);
      if (!cleared)
        setError(
          "Поручение подтверждено сервером, но локальная запись браузера недоступна. Проверьте историю отчётов.",
        );
      setPending(null);
      setPreview(null);
      setResult(work);
      setConsent(false);
      void history.refetch();
    } catch (cause) {
      if (
        cause instanceof ApiError &&
        [400, 403, 409, 410, 422].includes(cause.status)
      ) {
        const cleared = clearPendingStorage(scope);
        setPending(null);
        setPreview(null);
        setError(
          orderRequestError(cause) +
            (cleared
              ? ""
              : " Локальная запись подтверждения недоступна; проверьте настройки браузера."),
        );
      } else
        setError(
          "Ответ на подтверждение не получен. Черновик мог сохраниться. Повторная запись заблокирована; обновите историю отчётов и дождитесь сверки.",
        );
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <div className="settings-card settings-commission-import">
      <h3>Черновик отчёта комиссионера в МойСклад</h3>
      <p>
        Проверка читает закрытый месячный отчёт Ozon и готовит точный
        непроведённый документ по вашему финансовому разрешению. Создание
        начинается только после отдельного подтверждения или по включённой
        автоматизации.
      </p>
      {!active && (
        <p className="settings-info">
          Финансовое разрешение выключено. Настройте договор, валюту, цену и НДС
          в блоке «Черновики отчётов комиссионера» выше и сохраните настройки.
        </p>
      )}
      <div className="settings-realization-controls">
        <label>
          Месяц черновика комиссионера
          <input
            type="month"
            min="2023-08"
            max={maximum}
            value={period}
            disabled={busy || !!pending}
            onChange={(event) => {
              setPeriod(event.target.value);
              setPreview(null);
              setConsent(false);
              setRetryConsent(false);
            }}
          />
        </label>
        <button
          type="button"
          className="secondary"
          disabled={!validPeriod || busy || !!pending || history.isError}
          onClick={() => void check()}
        >
          {busy ? "Проверяем…" : "Проверить черновик комиссионера"}
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {pending && (
        <p className="settings-warning" role="status">
          Результат подтверждения за {pending.period} пока не установлен.
          Повторная запись недоступна. Обновление истории только читает
          сохранённые данные; STYK отдельно сверяет внешний документ.
        </p>
      )}
      {preview && (
        <section aria-label="Проверка финансового черновика">
          {plan?.id && preview.validation.length === 0 && (
            <CommissionPlanSummary plan={plan} />
          )}
          {(preview.analysis ?? plan?.accounting) && (
            <OrdersFinancialLedger
              key={plan?.fingerprint ?? "source-analysis"}
              ledger={(preview.analysis ?? plan?.accounting)!}
              planned
            />
          )}
          {preview.validation.map((issue, index) => (
            <p className="settings-warning" key={`${issue.code}:${index}`}>
              {issueLabel(issue.code)}
            </p>
          ))}
          {changed && (
            <p className="settings-warning">
              Исходный отчёт изменился после предыдущей операции. Требуется
              ручной разбор корректировки; существующий документ не
              перезаписывается.
            </p>
          )}
          {changed && preview.source_diff && (
            <CommissionSourceDiff diff={preview.source_diff} />
          )}
          {existing && <CommissionWorkResult work={existing} />}
          {expired && plan?.id && (
            <p className="settings-warning">
              Срок проверки истёк. Подготовьте новую проверку перед созданием.
            </p>
          )}
          {ready && (
            <>
              <label className="orders-check">
                <input
                  type="checkbox"
                  checked={consent}
                  disabled={busy || !!pending}
                  onChange={(event) => setConsent(event.target.checked)}
                />
                Я проверил полный период, договор, валюту, цены, НДС и позиции и
                разрешаю создать только непроведённый черновик отчёта
                комиссионера. Неучтённые компоненты остаются для ручной сверки;
                финансовый возврат не означает поступление товара на склад.
              </label>
              {retry && (
                <label className="orders-check orders-warning">
                  <input
                    type="checkbox"
                    checked={retryConsent}
                    disabled={busy || !!pending}
                    onChange={(event) => setRetryConsent(event.target.checked)}
                  />
                  Подтверждаю новую попытку после доказанного отказа до
                  отправки. Исходная операция и её история сохраняются.
                </label>
              )}
              <button
                type="button"
                disabled={
                  busy || !!pending || !consent || (retry && !retryConsent)
                }
                onClick={() => void approve()}
              >
                {retry
                  ? "Создать новую попытку черновика"
                  : "Создать непроведённый черновик"}
              </button>
            </>
          )}
        </section>
      )}
      {result && !history.data?.some((work) => work.id === result.id) && (
        <CommissionWorkResult work={result} />
      )}
      <div className="settings-card-title">
        <h4>История финансовых черновиков</h4>
        <button
          type="button"
          className="text-action"
          disabled={history.isFetching}
          onClick={() => void history.refetch()}
        >
          Обновить историю отчётов
        </button>
      </div>
      {history.isFetching && (
        <p role="status">Читаем сохранённую историю отчётов…</p>
      )}
      {history.isError && (
        <p role="alert">{orderRequestError(history.error)}</p>
      )}
      {history.data?.length === 0 && <p>Финансовых операций пока нет.</p>}
      {history.data?.map((work) => (
        <CommissionWorkResult work={work} key={work.id} />
      ))}
    </div>
  );
}
