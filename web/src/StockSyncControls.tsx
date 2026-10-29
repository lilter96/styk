import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, params, post } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";

type SyncScope = {
  tenant: string;
  moysklad_account: string;
  ozon_account: string;
};
type Check = { code: string; passed: boolean; message: string };
type Target = {
  ozon_product_id: string;
  ozon_warehouse_id: string;
  source_item_id: string;
  source_warehouse_id: string;
  quantity?: string;
  source_physical?: string;
  source_reserve?: string;
  marketplace_free?: number;
  marketplace_present?: number;
  marketplace_reserved?: number;
  status: string;
  checks: Check[];
  source_import_id?: number;
  settlement_operation_id?: string;
  settlement_observed_at?: string;
  product_mapping_version: number;
  warehouse_mapping_version: number;
};
type Publication = SyncScope & {
  id: string;
  state: string;
  created_at: string;
  targets: {
    id: number;
    ozon_product_id: string;
    ozon_warehouse_id: string;
    quantity: string;
    state: string;
    safe_error?: string;
    http_status?: number;
    acknowledged_at?: string;
    readback_at?: string;
    readback_quantity?: string;
  }[];
};
export type StockSyncView = SyncScope & {
  enabled: boolean;
  state: string;
  ready: boolean;
  checks: Check[];
  targets: Target[];
  last_operation?: Publication;
  last_confirmed_at?: string;
  publication_available?: boolean;
  test_account_mode?: boolean;
};
type Review = SyncScope & {
  review_id: string;
  ready: boolean;
  checks: Check[];
  targets: Target[];
  created_at: string;
  expires_at: string;
};
type SyncIntent = {
  scope: Readonly<SyncScope>;
  body: string;
  path: string;
  kind: "enable" | "disable" | "reconcile";
  uncertain: boolean;
};
const sameScope = (a: SyncScope, b: SyncScope) =>
  a.tenant === b.tenant &&
  a.moysklad_account === b.moysklad_account &&
  a.ozon_account === b.ozon_account;
const errors: Record<string, string> = {
  STOCK_PUBLICATION_DISABLED:
    "Отправка остатков временно недоступна: проверка безопасности ещё не завершена.",
  STOCK_SYNC_BLOCKED:
    "Условия отправки не подтверждены. Проверьте товары, склады и данные об остатках.",
  STOCK_SYNC_STALE:
    "Данные или соответствия изменились. Проверьте условия заново перед отправкой.",
  STOCK_SYNC_NOT_FOUND:
    "Настройка или отправка не найдена для выбранных аккаунтов.",
  INVALID_REQUEST: "Проверьте значения и подтверждения условий.",
  INVALID_SCOPE: "Выберите оба аккаунта этого рабочего пространства.",
  FORBIDDEN: "Запрос отклонён. Обновите страницу и войдите снова.",
};
export function useStockSyncSubmission() {
  const client = useQueryClient();
  const [pending, setPending] = useState<SyncIntent | null>(null),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState(""),
    [code, setCode] = useState(""),
    [inspected, setInspected] = useState(false);
  const [completed, setCompleted] = useState<{
    scope: SyncScope;
    sequence: number;
  } | null>(null);
  const [feedbackScope, setFeedbackScope] = useState<SyncScope | null>(null);
  const ref = useRef<SyncIntent | null>(null),
    lock = useRef(false);
  async function send(intent: SyncIntent) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    setCode("");
    try {
      const response = await fetch(intent.path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: intent.body,
        signal: AbortSignal.timeout(30000),
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new ApiError(response.status, data.error);
      }
      const data = (await response.json()) as StockSyncView & {
        operation?: Publication;
      };
      const actual = data.operation || data;
      if (!sameScope(actual, intent.scope))
        throw new Error("Scoped response unavailable");
      ref.current = null;
      setPending(null);
      setCompleted((old) => ({
        scope: intent.scope,
        sequence: (old?.sequence || 0) + 1,
      }));
      setMessage(
        intent.kind === "enable"
          ? "Синхронизация включена. Результат отправки появится после ответа Ozon."
          : intent.kind === "disable"
            ? "Отключение записано. Уже отправленный запрос может ещё выполняться."
            : "Состояние остатков проверено без повторной отправки.",
      );
      await client.invalidateQueries({
        queryKey: [
          "stock-sync",
          intent.scope.tenant,
          intent.scope.moysklad_account,
          intent.scope.ozon_account,
        ],
      });
    } catch (error) {
      const safeCode = error instanceof ApiError ? error.code || "" : "";
      setCode(safeCode);
      if (!intent.uncertain && errors[safeCode]) {
        ref.current = null;
        setPending(null);
        setMessage(errors[safeCode]);
      } else {
        intent.uncertain = true;
        setPending({ ...intent });
        setInspected(false);
        setMessage(
          "Результат запроса неизвестен. Не создавайте новую отправку. Повторите тот же запрос для восстановления его результата или проверьте состояние.",
        );
        if (error instanceof ApiError && error.status === 401)
          void client.invalidateQueries({ queryKey: ["session"] });
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function start(
    scope: SyncScope,
    kind: SyncIntent["kind"],
    reviewID?: string,
    operationID?: string,
  ) {
    if (ref.current || lock.current) return;
    const value: Record<string, unknown> = { ...scope, acknowledge: true };
    if (kind !== "reconcile") value.idempotency_key = crypto.randomUUID();
    if (kind === "enable") value.review_id = reviewID;
    const intent: SyncIntent = {
      scope: Object.freeze({ ...scope }),
      kind,
      body: JSON.stringify(value),
      path:
        kind === "reconcile"
          ? `/api/stock-sync/operations/${operationID}/reconcile`
          : `/api/stock-sync/${kind}`,
      uncertain: false,
    };
    ref.current = intent;
    setFeedbackScope(scope);
    setPending(intent);
    setInspected(false);
    void send(intent);
  }
  async function inspect() {
    const intent = ref.current;
    if (!intent || lock.current) return;
    lock.current = true;
    setBusy(true);
    try {
      const inspectedView = await api<StockSyncView>(
        `/api/stock-sync?${params(intent.scope)}`,
      );
      if (!sameScope(inspectedView, intent.scope))
        throw new Error("Scoped status unavailable");
      setInspected(true);
      setMessage(
        "Состояние проверено. Если ответ на включение потерян, восстановите его тем же запросом. Не повторяйте внешнюю отправку с новым ключом.",
      );
      await client.invalidateQueries({
        queryKey: [
          "stock-sync",
          intent.scope.tenant,
          intent.scope.moysklad_account,
          intent.scope.ozon_account,
        ],
      });
    } catch {
      setMessage(
        "Состояние пока недоступно. Дождитесь проверки перед новым действием.",
      );
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return {
    completed,
    feedbackScope,
    pending,
    busy,
    message,
    code,
    inspected,
    start,
    inspect,
    retry: () => {
      if (ref.current && code !== "IDEMPOTENCY_CONFLICT")
        void send(ref.current);
    },
    dismiss: () => {
      if (inspected && !lock.current) {
        ref.current = null;
        setPending(null);
        setMessage("");
        setCode("");
      }
    },
  };
}
export type StockSyncSubmissionState = ReturnType<
  typeof useStockSyncSubmission
>;
const proofLabels: Record<string, string> = {
  CUSTOMER_ORDER_COVERAGE_VERIFIED:
    "Все заказы и резервы сверены; их влияние на исходный остаток подтверждено независимой проверкой",
  SALE_UNIT_UNVERIFIED:
    "Единица продажи — одна штука; комплектов и пересчёта единиц нет",
  SOURCE_BASELINE_NOT_PROVEN:
    "Состав исходных остатков и полнота доступа проверены",
  SOURCE_ORDERING_UNPROVEN:
    "Учтён порядок изменений и граница актуальности исходных данных",
  UNASSIGNED_RESERVE_COVERAGE_MISSING:
    "Резервы без указания склада полностью учтены",
  RESERVATION_ATTRIBUTION_UNKNOWN:
    "Пересечение резервов МойСклад и обязательств Ozon проверено",
  POSTING_COVERAGE_INSUFFICIENT:
    "Учтены все обязательства Ozon, включая заказы до создания отправления",
  COMPETING_EXPOSURE_NOT_VERIFIED:
    "Другие каналы продаж и системы не расходуют этот остаток без учёта",
  UNSEEN_REDUCTION_BOUND_UNPROVEN:
    "Установлена отдельная верхняя граница ещё не учтённых уменьшений",
};
const checkLabels: Record<string, string> = {
  ...proofLabels,
  SOURCE_TRAVERSAL_STALE: "Повторите загрузку МойСклад: её начало устарело",
  OZON_TRAVERSAL_STALE: "Повторите загрузку Ozon: её начало устарело",
  PROOF_AFTER_SOURCE_TRAVERSAL_STARTED:
    "Подтвердите условия до начала новой загрузки МойСклад",
  OUT_OF_ORDER_IMPORT:
    "Загрузки завершились в неверном порядке — повторите загрузку данных",
  SOURCE_IDENTITY_UNCONFIRMED:
    "Товар и склад должны присутствовать в актуальной загрузке МойСклад",
  CATALOG_IDENTITY_CHANGED:
    "Проверьте соответствие товара, предложения и SKU в актуальном каталоге Ozon",
  WAREHOUSE_SCHEMA_UNCONFIRMED: "Проверьте, что склад относится к схеме FBS",
  MAPPINGS_REQUIRED: "Подтвердите соответствия товаров и складов",
  CONNECTION_DISABLED: "Подключение отключено. Обновите данные доступа",
  SYNC_CONDITIONS_CHANGED:
    "Соответствия или настройки изменились — проверьте условия заново",
  OZON_TARGET_IDENTITIES: "Идентификаторы товара и склада Ozon проверены",
  OZON_STOCK_VALUES_INVALID: "Проверьте значения остатков и резервов Ozon",
  OZON_STOCK_BASELINE_REQUIRED:
    "Есть данные Ozon об остатке этого товара на складе",
  OZON_BASELINE_FRESH: "Данные Ozon получены не более пятнадцати минут назад",
  PUBLICATION_QUANTITY_VALID: "Остаток рассчитан в целых штуках без округления",
  ALL_TARGETS_READY:
    "Условия подтверждены для всех выбранных товаров и складов",
  ATTESTATION_TARGETS_CHANGED:
    "Набор сопоставленных товаров или складов изменился",

  OWNERSHIP_NOT_REGISTERED: "Аккаунты принадлежат этому рабочему пространству",
  PRODUCT_MAPPING_NOT_CONFIRMED: "Соответствия товаров подтверждены",
  WAREHOUSE_MAPPING_NOT_CONFIRMED: "Соответствия складов подтверждены",
  NO_MAPPINGS: "Нет подтверждённых соответствий товаров и складов",
  SOURCE_OBSERVATION_MISSING: "Есть данные об остатках по выбранному складу",
  SOURCE_OBSERVATION_STALE: "Исходные остатки достаточно свежие",
  SOURCE_TRAVERSAL_INCOMPLETE: "Загрузка МойСклад завершена",
  PHYSICAL_STOCK_MISSING: "Известен физический остаток",
  SOURCE_RESERVE_MISSING: "Известен резерв МойСклад",
  UNREFLECTED_OBLIGATIONS_MISSING: "Определены ещё не учтённые обязательства",
  UNSEEN_REDUCTION_BOUND_MISSING: "Определён запас на неизвестные изменения",
  SHARED_POOL_UNSUPPORTED:
    "Один физический остаток не распределён между несколькими предложениями",
  UNCERTAIN_WRITER_SETTLEMENT_REQUIRED:
    "Подтвердите завершение предыдущего неопределённого запроса и получите свежие исходные данные",
  CREDENTIALS_UNAVAILABLE: "Доступ к обоим сервисам настроен",
  TARGET_LIMIT_REACHED: "Число товаров укладывается в лимит одной проверки",
};
const humanCheck = (check: Check) =>
  checkLabels[check.code] ||
  "Требуется дополнительная проверка данных и правил учёта";
type EvidenceForm = {
  unreflected: string;
  unseen_bound: string;
  safety_stock: string;
  reference: string;
  asOf: string;
  minutes: string;
  proofs: Record<string, boolean>;
};
type TestAttestation = {
  ozon_product_id: string;
  ozon_warehouse_id: string;
  unreflected: string;
  unseen_bound: string;
  safety_stock: string;
  proofs: {
    code: string;
    reference: string;
    as_of: string;
    valid_until: string;
  }[];
};
const freshEvidence = (): EvidenceForm => ({
  unreflected: "",
  unseen_bound: "",
  safety_stock: "",
  reference: "",
  asOf: "",
  minutes: "5",
  proofs: {},
});
export function StockSyncControls({
  tenant,
  moy,
  ozon,
  eligible,
  submission,
  onStatus,
}: {
  tenant: string;
  moy: string;
  ozon: string;
  eligible: boolean;
  submission?: StockSyncSubmissionState;
  onStatus?: (view: StockSyncView) => void;
}) {
  const local = useStockSyncSubmission(),
    commands = submission || local;
  const scope: SyncScope = {
    tenant,
    moysklad_account: moy,
    ozon_account: ozon,
  };
  const client = useQueryClient();
  const [open, setOpen] = useState<"enable" | "disable" | null>(null),
    [review, setReview] = useState<Review | null>(null),
    [previewBusy, setPreviewBusy] = useState(false),
    [message, setMessage] = useState(""),
    [ack, setAck] = useState(false),
    [advanced, setAdvanced] = useState(false),
    [evidence, setEvidence] = useState<Record<string, EvidenceForm>>({}),
    [testAck, setTestAck] = useState(false),
    [testAttestations, setTestAttestations] = useState<TestAttestation[]>([]);
  const scopeKey = `${tenant}:${moy}:${ozon}`;
  const query = useQuery({
    queryKey: ["stock-sync", tenant, moy, ozon],
    queryFn: ({ signal }) =>
      api<StockSyncView>(`/api/stock-sync?${params(scope)}`, signal),
    enabled: !!tenant && !!moy && !!ozon,
    retry: false,
    refetchInterval: (q) =>
      !q.state.error &&
      q.state.data &&
      (q.state.data.enabled ||
        ["QUEUED", "RUNNING", "OUTCOME_UNKNOWN", "PARTIAL"].includes(
          q.state.data.last_operation?.state || "",
        ))
        ? 2000
        : false,
  });
  const view = query.data;
  const notify = useCallback(
    (value: StockSyncView) => onStatus?.(value),
    [onStatus],
  );
  useEffect(() => {
    if (view) notify(view);
  }, [view, notify]);
  useEffect(() => {
    setOpen(null);
    setReview(null);
    setEvidence({});
    setTestAck(false);
    setTestAttestations([]);
    setAck(false);
    setMessage("");
  }, [scopeKey]);
  useEffect(() => {
    if (commands.completed && sameScope(commands.completed.scope, scope)) {
      setOpen(null);
      setReview(null);
      setAck(false);
    }
  }, [commands.completed?.sequence, scopeKey]);
  async function preview(withEvidence = false, testInput?: TestAttestation[]) {
    if (previewBusy) return;
    setPreviewBusy(true);
    setMessage("");
    setAck(false);
    try {
      const attestations =
        testInput ||
        (!withEvidence && view?.test_account_mode && testAttestations.length
          ? testAttestations
          : undefined) ||
        (withEvidence
          ? (review?.targets || []).map((target) => {
              const form =
                evidence[
                  `${target.ozon_product_id}:${target.ozon_warehouse_id}`
                ] || freshEvidence();
              if (
                !form.reference.trim() ||
                form.reference.trim().length < 16 ||
                !form.asOf ||
                ![form.unreflected, form.unseen_bound, form.safety_stock].every(
                  (value) => /^\d+$/.test(value),
                ) ||
                !/^\d+$/.test(form.minutes) ||
                Number(form.minutes) < 1 ||
                Number(form.minutes) > 15
              )
                throw new Error(
                  "Укажите проверенные количества, время и основание проверки каждого товара. Пустые значения не считаются нулём.",
                );
              return {
                ozon_product_id: target.ozon_product_id,
                ozon_warehouse_id: target.ozon_warehouse_id,
                unreflected: form.unreflected,
                unseen_bound: form.unseen_bound,
                safety_stock: form.safety_stock,
                proofs: Object.keys(proofLabels)
                  .filter((code) => form.proofs[code])
                  .map((code) => ({
                    code,
                    reference: form.reference.trim(),
                    as_of: new Date(form.asOf).toISOString(),
                    valid_until: new Date(
                      Date.now() + Number(form.minutes) * 60000,
                    ).toISOString(),
                  })),
              };
            })
          : undefined);
      const result = await post<Review>(
        "/api/stock-sync/preview",
        { ...scope, ...(attestations ? { attestations } : {}) },
        AbortSignal.timeout(30000),
      );
      if (!sameScope(result, scope))
        throw new Error(
          "Не удалось проверить область данных. Обновите страницу.",
        );
      setReview(result);
      setOpen("enable");
    } catch (error) {
      setMessage(
        error instanceof ApiError
          ? errors[error.code || ""] ||
              "Не удалось проверить условия отправки. Обновите подключения и данные."
          : error instanceof Error
            ? error.message
            : "Не удалось проверить условия отправки.",
      );
    } finally {
      setPreviewBusy(false);
    }
  }
  function attestTestConditions() {
    if (!testAck || !view?.test_account_mode || review?.targets.length !== 1)
      return;
    const now = new Date();
    const until = new Date(now.getTime() + 10 * 60000).toISOString();
    const target = review.targets[0];
    const reference =
      "OPERATOR_ATTESTED: controlled test policy; one item per unit; no unreflected obligations, competing exposure or unseen reductions assumed; order coverage not independently verified";
    const attestations: TestAttestation[] = [
      {
        ozon_product_id: target.ozon_product_id,
        ozon_warehouse_id: target.ozon_warehouse_id,
        unreflected: "0",
        unseen_bound: "0",
        safety_stock: "0",
        proofs: Object.keys(proofLabels).map((code) => ({
          code,
          reference,
          as_of: now.toISOString(),
          valid_until: until,
        })),
      },
    ];
    setTestAttestations(attestations);
    void preview(false, attestations);
  }
  const scopedPending =
    commands.pending && sameScope(commands.pending.scope, scope);
  const active = view?.enabled;
  const state = view?.state || "DISABLED";
  const uncertainOperation =
    ["OUTCOME_UNKNOWN", "PARTIAL"].includes(
      view?.last_operation?.state || "",
    ) || state.includes("UNKNOWN");
  const readbackRequired = state === "READBACK_REQUIRED";
  const settlementRequired = state === "EFFECT_CONFIRMED_REVIEW_REQUIRED";
  const deploymentBlocked = !!view && view.publication_available !== true;
  const blocked = !!active && (!view?.ready || state === "BLOCKED");
  const inFlightDuringHold =
    deploymentBlocked &&
    (state === "RUNNING" ||
      state === "UPDATING" ||
      state === "STOPPING" ||
      view?.last_operation?.state === "RUNNING");
  const label = readbackRequired
    ? "Требуется проверка"
    : state === "RECONCILING"
      ? "Проверяем результат"
      : state === "RECONCILED"
        ? "Синхронизировано"
        : state === "QUEUED"
          ? "Ожидает отправки"
          : uncertainOperation
            ? "Результат уточняется"
            : settlementRequired
              ? "Нужна проверка перед включением"
              : inFlightDuringHold
                ? "Останавливается"
                : deploymentBlocked
                  ? "Недоступна"
                  : blocked
                    ? "Приостановлена"
                    : active
                      ? state === "ACTIVE" || state === "SUCCEEDED"
                        ? "Включена"
                        : "Идёт отправка"
                      : state === "STOPPING"
                        ? "Останавливается"
                        : "Выключена";
  const failedChecks = (review?.checks || []).filter((check) => !check.passed);
  const uniqueFailed = new Map<string, Check>();
  for (const check of [
    ...failedChecks,
    ...(review?.targets || []).flatMap((t) =>
      t.checks.filter((c) => !c.passed),
    ),
  ])
    if (!uniqueFailed.has(check.code)) uniqueFailed.set(check.code, check);
  function update(target: Target, patch: Partial<EvidenceForm>) {
    const key = `${target.ozon_product_id}:${target.ozon_warehouse_id}`;
    setEvidence((old) => ({
      ...old,
      [key]: { ...(old[key] || freshEvidence()), ...patch },
    }));
  }
  // The controlled test policy selects its single approved mapping; unrelated
  // catalog items need not all be mapped to review that one target.
  if (!eligible && !view?.enabled && !view?.test_account_mode && !scopedPending)
    return null;
  return (
    <>
      <section className="sync-card" aria-label="Синхронизация остатков">
        <div>
          <div className="provider-heading">
            <h2>Синхронизация остатков</h2>
            <span
              className={`connection-state ${blocked || uncertainOperation || settlementRequired || deploymentBlocked ? "warning" : active && view?.ready ? "verified" : ""}`}
            >
              {label}
            </span>
          </div>
          <p>
            {uncertainOperation
              ? "Уточните результат предыдущей отправки. Повторная отправка в Ozon не запускается автоматически."
              : settlementRequired
                ? "При проверке Ozon вернул ожидаемый остаток. Это не подтверждает актуальность ответа или завершение прежнего запроса. Автоматическая отправка остановлена; перед включением нужна новая проверка."
                : inFlightDuringHold
                  ? "Ранее отправленный запрос ещё обрабатывается и может изменить остатки. Новые отправки недоступны."
                  : deploymentBlocked
                    ? "Отправка остатков временно недоступна: проверка безопасности ещё не завершена."
                    : blocked
                      ? "Новые отправки приостановлены: нужно заново проверить условия и актуальность данных."
                      : view?.last_confirmed_at
                        ? `Последнее подтверждённое обновление — ${new Date(view.last_confirmed_at).toLocaleString("ru-RU")}.`
                        : active
                          ? "Отправка выполняется только для проверенных данных и подтверждённых соответствий."
                          : "Остатки в Ozon изменятся только после явного включения и проверки условий."}
          </p>
          {view?.last_operation && (
            <p>
              Отправлено: {view.last_operation.targets[0]?.quantity ?? "—"} шт.
              {view.last_operation.targets[0]?.readback_quantity !==
                undefined && (
                <>
                  {" "}
                  · Проверено в Ozon:{" "}
                  {view.last_operation.targets[0].readback_quantity} шт. ·{" "}
                  {new Date(
                    view.last_operation.targets[0].readback_at!,
                  ).toLocaleString("ru-RU")}
                </>
              )}
            </p>
          )}
          {view?.last_operation && (
            <details className="technical-details">
              <summary>Технические детали</summary>
              <dl>
                <div>
                  <dt>Операция отправки</dt>
                  <dd>
                    <code>{view.last_operation.id}</code>
                  </dd>
                </div>
                <div>
                  <dt>Состояние обработки</dt>
                  <dd>
                    <code>{view.last_operation.state}</code>
                  </dd>
                </div>
              </dl>
              <button
                className="text-action"
                onClick={() =>
                  void navigator.clipboard
                    .writeText(view.last_operation!.id)
                    .catch(() => {})
                }
              >
                Копировать номер операции
              </button>
              {view.last_operation.targets.map((target) => (
                <p key={target.id}>
                  Товар <code>{target.ozon_product_id}</code> · склад{" "}
                  <code>{target.ozon_warehouse_id}</code> · {target.quantity}{" "}
                  шт. · <code>{target.state}</code>
                  {target.safe_error && (
                    <>
                      {" "}
                      · <code>{target.safe_error}</code>
                    </>
                  )}
                </p>
              ))}
            </details>
          )}
          {query.error && (
            <p className="is-error" role="alert">
              Не удалось уточнить состояние отправки.{" "}
              <button
                className="text-action"
                onClick={() => void query.refetch()}
              >
                Обновить статус
              </button>
            </p>
          )}
        </div>
        <div className="sync-actions">
          {blocked && !uncertainOperation && !deploymentBlocked && (
            <button
              disabled={!!commands.pending || commands.busy || !!query.error}
              onClick={() => {
                setOpen("enable");
                void preview();
              }}
            >
              Проверить условия
            </button>
          )}
          {active &&
            view?.test_account_mode &&
            !blocked &&
            !uncertainOperation &&
            !readbackRequired &&
            state !== "QUEUED" &&
            state !== "RUNNING" &&
            state !== "RECONCILING" && (
              <button
                disabled={!!commands.pending || commands.busy || !!query.error}
                onClick={() => {
                  setOpen("enable");
                  void preview();
                }}
              >
                Проверить новую отправку
              </button>
            )}
          {active ? (
            <button
              className="secondary"
              disabled={!!commands.pending}
              onClick={() => {
                setAck(false);
                setOpen("disable");
              }}
            >
              Выключить
            </button>
          ) : (
            <button
              disabled={
                !!query.error ||
                query.isLoading ||
                !!commands.pending ||
                uncertainOperation ||
                deploymentBlocked
              }
              onClick={() => {
                setOpen("enable");
                void preview();
              }}
            >
              Включить синхронизацию
            </button>
          )}
          {view?.last_operation && (uncertainOperation || readbackRequired) && (
            <button
              className="secondary"
              disabled={!!commands.pending || commands.busy}
              onClick={() =>
                commands.start(
                  scope,
                  "reconcile",
                  undefined,
                  view.last_operation!.id,
                )
              }
            >
              Уточнить результат
            </button>
          )}
        </div>
      </section>
      {scopedPending && (
        <div className="integration-feedback" role="alert">
          <p>
            {commands.message || "Проверяем запрос на изменение синхронизации…"}
          </p>
          {!commands.busy && (
            <div className="provider-actions">
              <button onClick={commands.retry}>Повторить тот же запрос</button>
              <button
                className="text-action"
                onClick={() => void commands.inspect()}
              >
                Проверить состояние
              </button>
              {commands.inspected && (
                <button className="text-action" onClick={commands.dismiss}>
                  Состояние проверено, закрыть запрос
                </button>
              )}
            </div>
          )}
        </div>
      )}
      {open && (
        <IntegrationDialog
          title={
            open === "enable"
              ? "Включить синхронизацию остатков?"
              : "Выключить синхронизацию?"
          }
          onClose={() => setOpen(null)}
        >
          <div className="provider-settings-body">
            <p>
              {open === "enable"
                ? view?.test_account_mode
                  ? "Для выбранного тестового товара выполняется одна отправка после подтверждения. Автоматическая повторная синхронизация не включается; положительный остаток может быть доступен покупателям."
                  : "После включения проверенные остатки из МойСклад будут отправляться в Ozon. Остатки на Ozon изменятся. Данные МойСклад не изменятся."
                : "Новые отправки будут остановлены. Уже отправленный запрос может завершиться; его результат останется в истории."}
            </p>
            {previewBusy && (
              <p role="status">
                Проверяем товары, склады и данные об остатках…
              </p>
            )}
            {message && (
              <p className="integration-feedback" role="alert">
                {message}
              </p>
            )}
            {commands.message &&
              commands.feedbackScope &&
              sameScope(commands.feedbackScope, scope) && (
                <p className="integration-feedback" role="alert">
                  {commands.message}
                </p>
              )}
            {scopedPending && !commands.busy && (
              <div className="provider-actions">
                <button className="secondary" onClick={commands.retry}>
                  Повторить тот же запрос
                </button>
                <button
                  className="text-action"
                  onClick={() => void commands.inspect()}
                >
                  Проверить состояние
                </button>
              </div>
            )}
            {open === "enable" && review && (
              <>
                {review.ready ? (
                  <ul className="sync-checklist">
                    <li>
                      ✓ Условия отправки подтверждены для{" "}
                      {review.targets.length} позиций по складам
                    </li>
                    <li>✓ Остатки в МойСклад не изменятся</li>
                    <li>✓ Синхронизацию можно выключить</li>
                  </ul>
                ) : (
                  <div className="integration-feedback">
                    <strong>Отправка пока недоступна</strong>
                    <p>
                      Часть условий не подтверждена. Неизвестные остатки не
                      заменяются нулём.
                    </p>
                    <ul className="sync-checklist">
                      {Array.from(uniqueFailed.values())
                        .slice(0, 8)
                        .map((check) => (
                          <li key={check.code}>• {humanCheck(check)}</li>
                        ))}
                    </ul>
                  </div>
                )}
                <details className="technical-details">
                  <summary>Проверенные товары и склады</summary>
                  {review.targets.map((target, index) => (
                    <div
                      className="sync-target"
                      key={`${target.ozon_product_id}:${target.ozon_warehouse_id}`}
                    >
                      <strong>
                        Позиция {index + 1}
                        {target.quantity !== undefined
                          ? ` · к отправке ${target.quantity} шт.`
                          : " · расчёт не подтверждён"}
                      </strong>
                      <p>
                        МойСклад: {target.source_physical ?? "неизвестно"} шт. ·
                        резерв: {target.source_reserve ?? "неизвестно"} шт. ·
                        Ozon свободно: {target.marketplace_free ?? "неизвестно"}{" "}
                        шт. · резерв:{" "}
                        {target.marketplace_reserved ?? "неизвестно"} шт.
                      </p>
                      <dl>
                        <div>
                          <dt>Товар Ozon</dt>
                          <dd>
                            <code>{target.ozon_product_id}</code>
                          </dd>
                        </div>
                        <div>
                          <dt>Склад Ozon</dt>
                          <dd>
                            <code>{target.ozon_warehouse_id}</code>
                          </dd>
                        </div>
                      </dl>
                      {target.settlement_operation_id && (
                        <div className="import-warning">
                          <p>
                            Перед новой отправкой проверьте, что прежний запрос
                            больше не может изменить остаток. В основании
                            проверки укажите номер операции ниже и время после
                            проверки текущего остатка. Затем заново загрузите
                            актуальные остатки.
                          </p>
                          <p>
                            Операция:{" "}
                            <code>{target.settlement_operation_id}</code>
                          </p>
                          <button
                            className="text-action"
                            onClick={() =>
                              void navigator.clipboard.writeText(
                                target.settlement_operation_id!,
                              )
                            }
                          >
                            Копировать номер операции
                          </button>
                          {target.settlement_observed_at && (
                            <p>
                              Остаток проверен:{" "}
                              {new Date(
                                target.settlement_observed_at,
                              ).toLocaleString("ru-RU")}
                            </p>
                          )}
                        </div>
                      )}
                      <ul className="sync-checklist">
                        {target.checks
                          .filter((check) => !check.passed)
                          .map((check) => (
                            <li key={check.code}>
                              {humanCheck(check)} <code>{check.code}</code>
                            </li>
                          ))}
                      </ul>
                    </div>
                  ))}
                </details>
                {!review.ready && review.targets.length > 0 && (
                  <button
                    className="text-action"
                    onClick={() => setAdvanced(!advanced)}
                  >
                    {advanced
                      ? "Скрыть условия"
                      : "Подтвердить условия вручную"}
                  </button>
                )}
                {view?.test_account_mode && review.targets.length === 1 && (
                  <div className="sync-evidence">
                    <p>
                      Для этой тестовой пары оператор принимает допущения:
                      единица продажи — 1 штука; неучтённые обязательства,
                      неизвестные уменьшения и страховой остаток равны 0; другие
                      каналы не расходуют этот запас. Полнота заказов и
                      отсутствие конкурирующих изменений не подтверждены
                      независимыми данными.
                    </p>
                    <label className="integration-ack">
                      <input
                        type="checkbox"
                        checked={testAck}
                        onChange={(e) => setTestAck(e.target.checked)}
                      />
                      Подтверждаю эти тестовые допущения и принимаю возможность
                      доступа покупателей к положительному остатку.
                    </label>
                    <button
                      className="secondary"
                      disabled={!testAck || previewBusy}
                      onClick={attestTestConditions}
                    >
                      Записать тестовые допущения
                    </button>
                    {testAttestations.length > 0 && (
                      <p>
                        Допущения записаны как утверждение оператора. После
                        этого загрузите данные МойСклад и Ozon заново и
                        повторите проверку в течение 10 минут.
                      </p>
                    )}
                  </div>
                )}
                {advanced &&
                  review.targets.map((target, index) => {
                    const form =
                      evidence[
                        `${target.ozon_product_id}:${target.ozon_warehouse_id}`
                      ] || freshEvidence();
                    return (
                      <fieldset
                        className="sync-evidence"
                        key={`${target.ozon_product_id}:${target.ozon_warehouse_id}`}
                      >
                        <legend>Проверка позиции {index + 1}</legend>
                        <p>
                          Заполняйте только на основании проверенных данных. Эти
                          сведения не заменяют автоматическую проверку
                          соответствий и свежести остатков.
                        </p>
                        <label className="integration-field">
                          Ещё не учтённые обязательства, шт.
                          <input
                            inputMode="numeric"
                            value={form.unreflected}
                            onChange={(e) =>
                              update(target, { unreflected: e.target.value })
                            }
                          />
                        </label>
                        <label className="integration-field">
                          Запас на ещё не учтённые уменьшения, шт.
                          <input
                            inputMode="numeric"
                            value={form.unseen_bound}
                            onChange={(e) =>
                              update(target, { unseen_bound: e.target.value })
                            }
                          />
                        </label>
                        <label className="integration-field">
                          Страховой остаток, шт.
                          <input
                            inputMode="numeric"
                            value={form.safety_stock}
                            onChange={(e) =>
                              update(target, { safety_stock: e.target.value })
                            }
                          />
                        </label>
                        <label className="integration-field">
                          На какое время подтверждены условия
                          <input
                            type="datetime-local"
                            value={form.asOf}
                            onChange={(e) =>
                              update(target, { asOf: e.target.value })
                            }
                          />
                        </label>
                        <label className="integration-field">
                          Срок действия подтверждения, минут
                          <input
                            type="number"
                            min={1}
                            max={15}
                            value={form.minutes}
                            onChange={(e) =>
                              update(target, { minutes: e.target.value })
                            }
                          />
                        </label>
                        <label className="integration-field">
                          Основание проверки без ключей доступа
                          <textarea
                            minLength={16}
                            maxLength={1000}
                            value={form.reference}
                            onChange={(e) =>
                              update(target, { reference: e.target.value })
                            }
                          />
                        </label>
                        {Object.entries(proofLabels).map(([code, text]) => (
                          <label className="integration-ack" key={code}>
                            <input
                              type="checkbox"
                              checked={!!form.proofs[code]}
                              onChange={(e) =>
                                update(target, {
                                  proofs: {
                                    ...form.proofs,
                                    [code]: e.target.checked,
                                  },
                                })
                              }
                            />
                            {text}
                          </label>
                        ))}
                      </fieldset>
                    );
                  })}
                {advanced && (
                  <button
                    className="secondary"
                    disabled={previewBusy}
                    onClick={() => void preview(true)}
                  >
                    Проверить подтверждённые условия
                  </button>
                )}
              </>
            )}
            {open === "enable" && !previewBusy && (
              <button
                className="text-action"
                onClick={() => void preview(advanced)}
              >
                Проверить условия заново
              </button>
            )}
            <label className="integration-ack">
              <input
                type="checkbox"
                checked={ack}
                onChange={(e) => setAck(e.target.checked)}
              />
              {open === "enable"
                ? "Подтверждаю изменение остатков в Ozon для выбранных аккаунтов"
                : "Подтверждаю отключение новых отправок"}
            </label>
            <div className="provider-actions">
              <button
                disabled={
                  !ack ||
                  commands.busy ||
                  !!commands.pending ||
                  previewBusy ||
                  (open === "enable" &&
                    (!review?.ready ||
                      Date.parse(review.expires_at) <= Date.now()))
                }
                onClick={() => {
                  if (open === "enable" && review)
                    commands.start(scope, "enable", review.review_id);
                  else if (open === "disable") commands.start(scope, "disable");
                  void client.invalidateQueries({
                    queryKey: ["stock-sync", tenant, moy, ozon],
                  });
                }}
              >
                {open === "enable"
                  ? view?.test_account_mode
                    ? "Отправить остаток"
                    : "Включить синхронизацию"
                  : "Выключить синхронизацию"}
              </button>
              <button className="secondary" onClick={() => setOpen(null)}>
                Отмена
              </button>
            </div>
            <p>
              При неизвестном результате запрос не отправляется в Ozon повторно
              автоматически. Проверка фактического остатка не доказывает
              завершение прежнего запроса; для возобновления требуется отдельная
              проверка.
            </p>
          </div>
        </IntegrationDialog>
      )}
    </>
  );
}
export function StockSyncNotice({
  submission,
  scope,
  onIntegrations,
  restore,
}: {
  submission: StockSyncSubmissionState;
  scope: SyncScope;
  onIntegrations: boolean;
  restore: (scope: SyncScope) => void;
}) {
  if (
    !submission.pending ||
    (onIntegrations && sameScope(submission.pending.scope, scope))
  )
    return null;
  return (
    <div className="integration-feedback" role="alert">
      <strong>Есть запрос на изменение синхронизации</strong>
      <p>
        {submission.message ||
          "Запрос относится к исходной паре аккаунтов. Смена пространства не отменяет его."}
      </p>
      <button
        className="secondary"
        onClick={() => restore(submission.pending!.scope)}
      >
        К исходным аккаунтам
      </button>
    </div>
  );
}
