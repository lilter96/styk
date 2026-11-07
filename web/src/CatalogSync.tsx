import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, post } from "./api";
import { CatalogSyncConfig } from "./CatalogSyncConfig";
import {
  CatalogSyncProducts,
  type ProductSelection,
} from "./CatalogSyncProducts";
import { CatalogSyncPreview } from "./CatalogSyncPreview";
import { CatalogSyncOperations } from "./CatalogSyncOperations";
import {
  catalogURL,
  configBody,
  normalizeConfig,
  normalizePreview,
  requestError,
  scopeBody,
  type CatalogConfig,
  type CatalogOperation,
  type CatalogPreview,
  type CatalogValue,
  type Scope,
  type ProductPage,
} from "./CatalogSyncTypes";
import "./catalog-sync.css";

export type CatalogSyncProps = {
  scope: Scope;
  view: "rules" | "transfer" | "automation";
  operationID?: string;
  onApproved?: (id: string) => void;
  initialProducts?: { id: string; offer: string }[];
};
const sameScope = (value: CatalogConfig["scope"], scope: Scope) =>
  value?.tenant === scope.tenant &&
  value.moysklad_account === scope.moy &&
  value.ozon_account === scope.ozon;

export function CatalogSync(props: CatalogSyncProps) {
  const { scope } = props;
  if (!scope.tenant || !scope.moy || !scope.ozon)
    return (
      <section className="catalog-sync">
        <h1>Синхронизация каталога</h1>
        <p className="catalog-empty">
          Подключите МойСклад и Ozon в разделе интеграций, чтобы настроить
          передачу данных о товарах.
        </p>
      </section>
    );
  return <CatalogSyncAccount key={JSON.stringify(scope)} {...props} />;
}

function CatalogSyncAccount({
  scope,
  view,
  initialProducts,
  operationID,
  onApproved,
}: CatalogSyncProps) {
  const client = useQueryClient();
  const configKey = ["catalog-config", scope];
  const config = useQuery({
    queryKey: configKey,
    queryFn: async ({ signal }) =>
      normalizeConfig(
        await api<CatalogConfig>(catalogURL("config", scope), signal),
      ),
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const [selected, setSelected] = useState<Record<string, ProductSelection>>(
    {},
  );
  const [preview, setPreview] = useState<CatalogPreview | null>(null);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState<"preview" | "approve" | null>(null);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [uncertain, setUncertain] = useState<string[]>([]);
  const lock = useRef(false);
  const selectionLoaded = useRef(false);
  useEffect(() => {
    const requested = initialProducts;
    if (view !== "transfer" || !requested?.length || selectionLoaded.current)
      return;
    const controller = new AbortController();
    setBusy("preview");
    void Promise.all(
      requested.slice(0, 20).map(async (item) => {
        const page = await api<ProductPage>(
          catalogURL("products", scope, {
            provider: "ozon",
            search: item.offer || item.id,
            limit: "100",
          }),
          controller.signal,
        );
        const product = page.products.find((p) => p.identity.id === item.id);
        const enrollment = page.enrollments?.find((e) =>
          [e.source, e.target].some(
            (i) => i.account.provider === "ozon" && i.id === item.id,
          ),
        );
        if (!product || !enrollment)
          throw new Error("selected product unavailable");
        return [
          enrollment.id,
          { product, enrollment, manual_values: {} },
        ] as const;
      }),
    )
      .then((entries) => {
        if (!controller.signal.aborted) {
          setSelected(Object.fromEntries(entries));
          selectionLoaded.current = true;
        }
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setError(
            "Не удалось загрузить выбранные связи. Выберите товары повторно.",
          );
      })
      .finally(() => {
        if (!controller.signal.aborted) setBusy(null);
      });
    return () => controller.abort();
  }, [initialProducts, scope, view]);
  const uncertainSelection = Object.keys(selected).some((id) =>
    uncertain.includes(id),
  );
  function saved(value: CatalogConfig) {
    if (!sameScope(value.scope, scope)) {
      setError(
        "Ответ не соответствует выбранным аккаунтам. Обновите страницу.",
      );
      return;
    }
    client.setQueryData(
      configKey,
      normalizeConfig({
        ...value,
        fields: value.fields ?? config.data?.fields,
        publication_available:
          value.publication_available ?? config.data?.publication_available,
      }),
    );
    setDirty(false);
    setPreview(null);
  }
  async function review() {
    if (
      lock.current ||
      !config.data ||
      dirty ||
      uncertainSelection ||
      !Object.keys(selected).length
    )
      return;
    lock.current = true;
    setBusy("preview");
    setError("");
    setMessage("");
    setPreview(null);
    try {
      let current = config.data;
      const updates = Object.entries(selected)
        .filter(([id, selection]) => {
          const existing = current.targets.find((target) => target.id === id);
          return (
            !existing ||
            enrollmentJSON(existing) !== enrollmentJSON(selection.enrollment)
          );
        })
        .map(([, selection]) => ({
          ...selection.enrollment,
          automatic: false,
          automatic_fields: [],
        }));
      if (updates.length) {
        current = normalizeConfig(
          await post<CatalogConfig>(
            "/api/catalog-sync/config",
            {
              ...scopeBody(scope),
              config: configBody({
                ...current,
                targets: [
                  ...current.targets.filter(
                    (target) =>
                      !updates.some((update) => update.id === target.id),
                  ),
                  ...updates,
                ],
              }),
              expected_revision: current.revision,
              idempotency_key: crypto.randomUUID(),
            },
            AbortSignal.timeout(30000),
          ),
        );
        if (!sameScope(current.scope, scope)) throw new Error("scope mismatch");
        saved(current);
      }
      const value = await post<CatalogPreview>(
        "/api/catalog-sync/preview",
        {
          ...scopeBody(scope),
          expected_revision: current.revision,
          enrollment_ids: Object.keys(selected),
          manual_values: manualValues(selected),
        },
        AbortSignal.timeout(30000),
      );
      if (
        !sameScope(value.scope, scope) ||
        value.config_revision !== current.revision
      )
        throw new Error("stale preview");
      setPreview(normalizePreview(value));
    } catch (error) {
      setError(requestError(error));
    } finally {
      lock.current = false;
      setBusy(null);
    }
  }
  async function approve(enrollmentIDs: string[]) {
    if (
      lock.current ||
      !preview ||
      dirty ||
      enrollmentIDs.some((id) => uncertain.includes(id)) ||
      Date.parse(preview.expires_at) <= Date.now()
    )
      return;
    lock.current = true;
    setBusy("approve");
    setError("");
    setMessage("");
    try {
      const operation = await post<CatalogOperation>(
        "/api/catalog-sync/approve",
        {
          ...scopeBody(scope),
          preview_id: preview.id,
          enrollment_ids: enrollmentIDs,
          acknowledge: true,
          idempotency_key: crypto.randomUUID(),
        },
        AbortSignal.timeout(30000),
      );
      if (!operation.id || !sameScope(operation.scope, scope))
        throw new Error("invalid approval acknowledgement");
      setPreview(null);
      setSelected({});
      onApproved?.(operation.id);
      setMessage(
        "Подтверждение записано. Ход отправки и результат сверки показаны ниже.",
      );
    } catch (error) {
      if (
        error instanceof ApiError &&
        [400, 403, 409, 410, 422].includes(error.status)
      ) {
        setPreview(null);
        setError(
          requestError(error) + " Нужна новая проверка перед отправкой.",
        );
      } else {
        setUncertain((previous) => [
          ...new Set([...previous, ...enrollmentIDs]),
        ]);
        setError(
          "Результат подтверждения неизвестен: запрос мог сохраниться. Проверьте статусы отправок. Не отправляйте эти изменения повторно до сверки результата.",
        );
      }
    } finally {
      await client.invalidateQueries({
        queryKey: ["catalog-operations", scope],
      });
      lock.current = false;
      setBusy(null);
    }
  }
  return (
    <div className="catalog-sync">
      {config.isLoading && <p role="status">Загружаем настройки каталога…</p>}
      {config.isError && (
        <p className="catalog-error" role="alert">
          {requestError(config.error)}{" "}
          <button className="text-action" onClick={() => void config.refetch()}>
            Повторить чтение настроек
          </button>
        </p>
      )}
      {config.data && !sameScope(config.data.scope, scope) && (
        <p className="catalog-error" role="alert">
          Настройки не соответствуют выбранным аккаунтам. Обновите страницу.
        </p>
      )}
      {config.data && sameScope(config.data.scope, scope) && (
        <>
          {(view === "rules" || view === "automation") && (
            <fieldset>
              <CatalogSyncConfig
                key={config.data.revision}
                scope={scope}
                automationOnly={view === "automation"}
                config={config.data}
                onSaved={saved}
                onDirty={(value) => {
                  setDirty(value);
                  setPreview(null);
                }}
              />
            </fieldset>
          )}
          {view === "transfer" && !operationID && (
            <>
              {!preview && (
                <CatalogSyncProducts
                  scope={scope}
                  config={config.data}
                  selected={selected}
                  disabled={busy !== null}
                  onChange={(next) => {
                    setSelected(next);
                    setPreview(null);
                    setMessage("");
                  }}
                />
              )}
              {!preview && (
                <div className="catalog-review-action">
                  <button
                    disabled={
                      busy !== null ||
                      dirty ||
                      uncertainSelection ||
                      !Object.keys(selected).length
                    }
                    onClick={() => void review()}
                  >
                    {busy === "preview"
                      ? "Проверяем изменения…"
                      : "Сохранить выбор и проверить"}
                  </button>
                  <p>
                    Выбор и настройки выбранных товаров будут сохранены для этой
                    пары аккаунтов с автоматической передачей, выключенной для
                    изменённых товаров. Изменения в сервисы не отправляются.
                  </p>
                </div>
              )}
              {(!config.data.enabled || !config.data.test_enabled) && (
                <p className="catalog-warning">
                  Перед отправкой включите синхронизацию каталога и сохраните
                  настройки. Само включение не подтверждает изменения новых
                  товаров.
                </p>
              )}
              {config.data.publication_available === false && (
                <p className="catalog-warning">
                  Отправка изменений в сервисы для этого аккаунта пока
                  недоступна.
                </p>
              )}
              {uncertainSelection && (
                <p className="catalog-warning" role="alert">
                  Для выбранного товара результат предыдущего подтверждения
                  неизвестен. Уберите его из выбора, чтобы проверить другие
                  товары.
                </p>
              )}
              {preview && preview.config_revision === config.data.revision && (
                <CatalogSyncPreview
                  key={preview.id}
                  preview={preview}
                  busy={busy === "approve"}
                  blocked={
                    uncertainSelection ||
                    dirty ||
                    !config.data.enabled ||
                    !config.data.test_enabled ||
                    config.data.publication_available === false
                  }
                  onApprove={(ids) => void approve(ids)}
                />
              )}
            </>
          )}
        </>
      )}
      {error && (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      )}
      {message && (
        <p className="catalog-success" role="status">
          {message}
        </p>
      )}
      {view === "transfer" && (
        <CatalogSyncOperations scope={scope} operationID={operationID} />
      )}
    </div>
  );
}

function manualValues(selected: Record<string, ProductSelection>) {
  return Object.fromEntries(
    Object.entries(selected).map(([id, selection]) => {
      const values: Record<string, CatalogValue> = {
        ...selection.manual_values,
      };
      if (selection.facts?.values) {
        Object.assign(values, selection.facts.values);
      }
      return [id, values];
    }),
  );
}

function enrollmentJSON(value: unknown): string {
  return JSON.stringify(value, (_key, item: unknown) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return item;
    return Object.fromEntries(
      Object.entries(item).sort(([left], [right]) => left.localeCompare(right)),
    );
  });
}
