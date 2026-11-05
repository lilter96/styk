import type { OrderTargetState } from "./OrdersTargetState";
import type { OrderNativeProposal } from "./OrdersNativeTypes";
import type { OrderCompositionProposal } from "./OrdersComposition";
import { useCallback, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, post } from "./api";
import { OrdersConfig } from "./OrdersConfig";
import { useOrdersMetadata } from "./useOrdersMetadata";
import { OrdersPostings } from "./OrdersPostings";
import { OrdersPreview } from "./OrdersPreview";
import { OrdersOperations } from "./OrdersOperations";
import { OrdersHeader } from "./OrdersHeader";
import {
  ordersURL,
  orderScopeBody,
  sameOrderScope,
  orderRequestError,
  type OrdersConfigData,
  type OrderScope,
  type OrderOperation,
  type OrdersPreviewData,
  type OrderActionProposal,
  type OrderActionOptions,
} from "./OrdersTypes";
import "./Orders.css";

export type OrdersProps = {
  scope?: OrderScope;
  accountScope?: OrderScope;
  moyLabel?: string;
  ozonLabel?: string;
};
export function Orders(props: OrdersProps) {
  const scope = props.accountScope ?? props.scope;
  if (!scope?.tenant || !scope.moy || !scope.ozon)
    return (
      <section className="orders-console">
        <OrdersHeader />
        <p className="orders-empty">
          Подключите МойСклад и Ozon в разделе интеграций, чтобы настроить
          передачу заказов.
        </p>
      </section>
    );
  return <OrdersAccount key={JSON.stringify(scope)} {...props} scope={scope} />;
}
export const OrdersPage = Orders;

function OrdersAccount({
  scope,
  moyLabel = "Подключённый аккаунт",
  ozonLabel = "Подключённый аккаунт",
}: OrdersProps & { scope: OrderScope }) {
  const client = useQueryClient();
  const configKey = ["orders-config", scope];
  const config = useQuery({
    queryKey: configKey,
    queryFn: async ({ signal }) => {
      const value = await api<OrdersConfigData>(
        ordersURL("config", scope),
        signal,
      );
      if (!sameOrderScope(value.scope, scope))
        throw new Error("config scope mismatch");
      return {
        ...value,
        interval_seconds: value.interval_seconds || 300,
        source_freshness_seconds: value.source_freshness_seconds || 300,
        preview_ttl_seconds: value.preview_ttl_seconds || 300,
        field_mappings: value.field_mappings ?? [],
        status_mappings: value.status_mappings ?? [],
      };
    },
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const [selected, setSelected] = useState<string[]>([]);
  const [unresolved, setUncertain] = useState<string[]>([]);
  const [recoveries, setRecoveries] = useState<Record<string, number>>({});
  const currentTargets = useRef<Record<string, OrderTargetState>>({});
  const uncertain = unresolved.filter(
    (number) =>
      currentTargets.current[number]?.unresolved ||
      !(recoveries[number] > Date.now()),
  );
  const recoveryAuthorization = useCallback(
    (posting: string, until: string | undefined) => {
      setRecoveries((previous) => ({
        ...previous,
        [posting]: until ? Date.parse(until) : 0,
      }));
      setPreview(null);
      void client.invalidateQueries({ queryKey: ["orders-postings", scope] });
    },
    [client, scope],
  );
  const [preview, setPreview] = useState<OrdersPreviewData | null>(null);
  const previewMetadata = useOrdersMetadata(
    scope,
    !!preview?.decisions.some((decision) =>
      (decision.commands ?? []).some(
        (command) =>
          !!command.customer_order &&
          !command.position_patch &&
          !command.position_replacement,
      ),
    ),
  );
  const [previewActions, setPreviewActions] = useState([
    "mirror_create",
    "mirror_update",
  ]);
  const [actionReset, setActionReset] = useState(0);
  const [busy, setBusy] = useState<"preview" | "approve" | "check" | null>(
    null,
  );
  const [dirty, setDirty] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [links, setLinks] = useState<Record<string, string>>({});
  const updateTargetStates = useCallback(
    (states: OrderTargetState[] | undefined) => {
      if (!states) {
        currentTargets.current = {};
        return;
      }
      const current = Object.fromEntries(
        states.map((state) => [state.posting_number, state]),
      );
      currentTargets.current = current;
      setUncertain((previous) => [
        ...new Set([
          ...previous.filter(
            (number) => !current[number] || current[number].unresolved,
          ),
          ...states
            .filter((state) => state.unresolved)
            .map((state) => state.posting_number),
        ]),
      ]);
      setLinks((previous) => ({
        ...previous,
        ...Object.fromEntries(
          states.map((state) => [
            state.posting_number,
            state.customer_order_id ?? "",
          ]),
        ),
      }));
    },
    [],
  );
  const updateLinks = useCallback(
    (value: Record<string, string>) =>
      setLinks((previous) => ({
        ...previous,
        ...Object.fromEntries(
          Object.entries(value).filter(
            ([number]) => !currentTargets.current[number],
          ),
        ),
      })),
    [],
  );
  const updateOutcomes = useCallback((states: Record<string, string>) => {
    setUncertain((previous) => [
      ...new Set([
        ...previous.filter(
          (number) =>
            currentTargets.current[number] ||
            !["RECONCILED", "REJECTED"].includes(states[number]),
        ),
        ...Object.keys(states).filter(
          (number) =>
            !currentTargets.current[number] && states[number] === "UNKNOWN",
        ),
      ]),
    ]);
  }, []);
  const lock = useRef(false);
  const canPublish =
    config.data?.enabled &&
    config.data.test_enabled &&
    config.data.publication_available === true;
  function saved(value: OrdersConfigData) {
    client.setQueryData(configKey, value);
    setDirty(false);
    setPreview(null);
    setMessage("Настройки заказов сохранены.");
  }
  async function review(
    proposal?:
      OrderActionProposal | OrderNativeProposal | OrderCompositionProposal,
  ) {
    if (proposal && dirty) {
      setError("Сохраните настройки перед проверкой изменения.");
      return;
    }
    const numbers = proposal ? [proposal.posting_number] : selected;
    const actions = proposal
      ? [proposal.action]
      : ["mirror_create", "mirror_update"];
    const allowedActions = actions.includes("mirror_update")
      ? [...actions, "mirror_position_update"]
      : actions;
    if (
      lock.current ||
      !config.data ||
      dirty ||
      !numbers.length ||
      numbers.some((number) => uncertain.includes(number))
    )
      return;
    lock.current = true;
    setBusy("preview");
    setError("");
    setMessage("");
    setPreview(null);
    setPreviewActions(allowedActions);
    try {
      const value = await post<OrdersPreviewData>(
        "/api/orders/preview",
        {
          ...orderScopeBody(scope),
          expected_revision: config.data.revision,
          posting_numbers: numbers,
          actions,
          ...(proposal
            ? "confirm_replacement" in proposal
              ? {
                  composition_proposals: [
                    {
                      posting_number: proposal.posting_number,
                      confirm_replacement: proposal.confirm_replacement,
                    },
                  ],
                }
              : "applicable" in proposal
                ? { native_proposals: [proposal] }
                : { proposals: [proposal] }
            : {}),
        },
        AbortSignal.timeout(30000),
      );
      if (
        !value.id ||
        !sameOrderScope(value.scope, scope) ||
        value.config_revision !== config.data.revision ||
        !Number.isFinite(Date.parse(value.expires_at)) ||
        value.decisions.some(
          (item) =>
            !numbers.includes(item.posting.posting_number) ||
            !sameOrderScope(item.posting.scope, scope) ||
            item.commands.some(
              (command) => !allowedActions.includes(command.action),
            ),
        )
      )
        throw new Error("invalid preview");
      setPreview(value);
    } catch (error) {
      setError(orderRequestError(error));
    } finally {
      lock.current = false;
      setBusy(null);
    }
  }
  async function approve(numbers: string[]) {
    if (
      lock.current ||
      !preview ||
      !numbers.length ||
      dirty ||
      !canPublish ||
      Date.parse(preview.expires_at) <= Date.now() ||
      numbers.some(
        (number) =>
          uncertain.includes(number) ||
          !preview.decisions.some(
            (decision) =>
              decision.posting.posting_number === number &&
              decision.ready &&
              !decision.validation?.length &&
              decision.commands.length > 0,
          ),
      )
    )
      return;
    lock.current = true;
    setBusy("approve");
    setError("");
    setMessage("");
    try {
      const value = await post<OrderOperation>(
        "/api/orders/approve",
        {
          ...orderScopeBody(scope),
          preview_id: preview.id,
          posting_numbers: numbers,
          idempotency_key: crypto.randomUUID(),
          acknowledge: true,
        },
        AbortSignal.timeout(30000),
      );
      if (!value.id || !sameOrderScope(value.scope, scope))
        throw new Error("invalid approval acknowledgement");
      setPreview(null);
      setSelected([]);
      setActionReset((value) => value + 1);
      setMessage(
        "Подтверждение записано. Результат передачи и сверки показан в истории.",
      );
    } catch (error) {
      if (
        error instanceof ApiError &&
        [400, 403, 409, 410, 422].includes(error.status)
      ) {
        setPreview(null);
        setError(orderRequestError(error));
      } else {
        setUncertain((previous) => [...new Set([...previous, ...numbers])]);
        setError(
          "Результат подтверждения неизвестен: запрос мог сохраниться. Проверьте историю передачи. Не отправляйте выбранные отправления повторно до сверки результата.",
        );
      }
    } finally {
      await client.invalidateQueries({
        queryKey: ["orders-operations", scope],
      });
      lock.current = false;
      setBusy(null);
    }
  }
  async function check() {
    if (lock.current || !config.data?.enabled || dirty) return;
    lock.current = true;
    setBusy("check");
    setError("");
    setMessage("");
    try {
      const value = await post<{ scheduled: boolean }>(
        "/api/orders/check",
        orderScopeBody(scope),
        AbortSignal.timeout(30000),
      );
      if (value.scheduled !== true) throw new Error("check not scheduled");
      setMessage(
        "Проверка источника запланирована. Обновите список отправлений после завершения чтения Ozon.",
      );
    } catch (error) {
      setError(orderRequestError(error));
    } finally {
      lock.current = false;
      setBusy(null);
    }
  }
  return (
    <div className="orders-console">
      <OrdersHeader moyLabel={moyLabel} ozonLabel={ozonLabel} />
      {config.isLoading && <p role="status">Загружаем настройки заказов…</p>}
      {config.isError && (
        <p role="alert" className="orders-error">
          {orderRequestError(config.error)}{" "}
          <button className="text-action" onClick={() => void config.refetch()}>
            Обновить настройки заказов
          </button>
        </p>
      )}
      {config.data && (
        <>
          {config.data.publication_available !== true && (
            <p className="orders-warning">
              Передача заказов для этих аккаунтов пока недоступна. Настройки и
              проверка данных доступны. Включение настройки не снимает
              ограничение передачи.
            </p>
          )}
          <OrdersConfig
            key={config.data.revision}
            scope={scope}
            moyLabel={moyLabel}
            config={config.data}
            onSaved={saved}
            onDirty={(value) => {
              setDirty(value);
              setPreview(null);
            }}
          />
          <div className="orders-source-action">
            <button
              type="button"
              className="secondary"
              disabled={busy !== null || dirty || !config.data.enabled}
              onClick={() => void check()}
            >
              {busy === "check"
                ? "Планируем проверку…"
                : "Проверить источник сейчас"}
            </button>
            <p>
              Проверка читает Ozon по сохранённым настройкам. Создание и
              обновление заказов требуют подтверждения или действующего
              разрешения автоматизации.
            </p>
          </div>
          {!config.data.enabled && (
            <p className="orders-warning">
              Передача выключена. Выберите реквизиты МойСклад, поля и сохраните
              настройки, чтобы запрашивать новые данные Ozon.
            </p>
          )}
          <OrdersPostings
            scope={scope}
            freshnessSeconds={config.data.source_freshness_seconds}
            actionReset={actionReset}
            onActionPreview={(proposal) => void review(proposal)}
            onNativePreview={(proposal) => void review(proposal)}
            onCompositionPreview={(proposal) => void review(proposal)}
            onActionDraftChange={() => setPreview(null)}
            selected={selected}
            disabled={busy !== null}
            uncertain={uncertain}
            links={links}
            onTargetStates={updateTargetStates}
            onSelected={(numbers) => {
              setSelected(numbers);
              setPreview(null);
              setMessage("");
            }}
          />
          <div className="orders-source-action">
            <button
              type="button"
              disabled={
                busy !== null ||
                dirty ||
                !selected.length ||
                selected.some((number) => uncertain.includes(number))
              }
              onClick={() => void review()}
            >
              {busy === "preview"
                ? "Проверяем изменения…"
                : "Проверить выбранные отправления"}
            </button>
            <p>
              {dirty
                ? "Сохраните настройки перед проверкой."
                : "Предпросмотр показывает изменения и блокировки для каждого отправления. Он не передаёт изменения в МойСклад."}
            </p>
          </div>
          {preview && (
            <OrdersPreview
              key={preview.id}
              preview={preview}
              permittedActions={previewActions}
              actionOptions={client
                .getQueriesData<OrderActionOptions>({
                  queryKey: ["orders-action-options", scope],
                })
                .flatMap(([, options]) => (options ? [options] : []))}
              metadata={previewMetadata.data}
              blocked={
                !canPublish ||
                dirty ||
                uncertain.some((number) =>
                  preview.decisions.some(
                    (decision) => decision.posting.posting_number === number,
                  ),
                )
              }
              busy={busy !== null}
              onApprove={(numbers) => void approve(numbers)}
            />
          )}
        </>
      )}
      {error && (
        <p role="alert" className="orders-error">
          {error}
        </p>
      )}
      {message && (
        <p role="status" className="orders-success">
          {message}
        </p>
      )}
      <OrdersOperations
        scope={scope}
        onLinks={updateLinks}
        onOutcomes={updateOutcomes}
        onRecoveryAuthorization={recoveryAuthorization}
      />
    </div>
  );
}
