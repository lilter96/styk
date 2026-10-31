import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  ApiError,
  params,
  post,
  type Batch,
  type ImportOperation,
} from "./api";
import type {
  CredentialMetadata,
  CredentialStatus,
} from "./CredentialSubmission";

export type Settings = { name: string; description: string; revision: number };
export type MoyProvider = {
  provider: string;
  account?: string;
  status: string;
  credentials_stored?: boolean;
  check?: { status?: string; checked_at: string; reason?: string };
  import?: Batch;
};
export const settingsDate = (value?: string | null) =>
  value &&
  Number.isFinite(Date.parse(value)) &&
  new Date(value).getUTCFullYear() > 1970
    ? new Date(value).toLocaleString("ru-RU", {
        day: "numeric",
        month: "short",
        year: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "Ещё не было";
export const importStates: Record<string, string> = {
  QUEUED: "В очереди",
  RUNNING: "Выполняется",
  SUCCEEDED: "Завершена",
  FAILED: "Ошибка загрузки",
  CANCELLED: "Отменена",
  OUTCOME_UNKNOWN: "Результат уточняется",
};
export function successfulCheck(
  provider?: MoyProvider,
  meta?: CredentialMetadata | null,
) {
  return [
    meta?.checked_at,
    provider?.status === "VERIFIED" ? provider.check?.checked_at : undefined,
  ]
    .filter(
      (value): value is string => !!value && Number.isFinite(Date.parse(value)),
    )
    .sort((a, b) => Date.parse(b) - Date.parse(a))[0];
}
export function connectionState(
  provider?: MoyProvider,
  meta?: CredentialMetadata | null,
) {
  if (meta && !meta.enabled) return { text: "Отключён", tone: "off" };
  if (
    !meta &&
    provider?.credentials_stored &&
    provider.status === "NOT_CONFIGURED"
  )
    return { text: "Отключён", tone: "off" };
  if (provider?.check?.reason === "INVALID_CREDENTIALS")
    return { text: "Ошибка авторизации", tone: "error" };
  if (provider?.status === "VERIFICATION_FAILED")
    return { text: "Требует проверки", tone: "warning" };
  if (provider?.status === "VERIFIED")
    return { text: "Подключён", tone: "success" };
  if (provider?.status === "CONFIGURED_NOT_VERIFIED")
    return { text: "Требует проверки", tone: "warning" };
  return { text: "Требует подключения", tone: "off" };
}
export function probeMessage(value: { status?: string; reason?: string }) {
  if (value.status === "VERIFIED")
    return "Подключение проверено. Доступ к МойСклад подтверждён.";
  const reasons: Record<string, string> = {
    INVALID_CREDENTIALS:
      "МойСклад отклонил токен. Замените его и повторите проверку.",
    PERMISSION_DENIED:
      "Недостаточно прав. Проверьте разрешения токена в МойСклад.",
    ACCOUNT_IDENTITY_MISMATCH: "Токен принадлежит другому аккаунту МойСклад.",
    NOT_CONFIGURED: "Сначала сохраните действующий токен.",
    TIMEOUT: "МойСклад не ответил вовремя. Повторите проверку позже.",
    PROBE_TIMEOUT: "МойСклад не ответил вовремя. Повторите проверку позже.",
  };
  return (
    reasons[value.reason || ""] ||
    "Не удалось проверить доступ к МойСклад. Повторите проверку позже."
  );
}
export function useMoySkladSettings(tenant: string, account: string) {
  const client = useQueryClient();
  const query = params({ tenant, account });
  const settings = useQuery({
    queryKey: ["moy-settings", tenant, account],
    queryFn: ({ signal }) =>
      api<Settings>(`/api/integrations/moysklad/settings?${query}`, signal),
    enabled: !!account,
    retry: false,
  });
  const credential = useQuery({
    queryKey: ["credentials", tenant, "moysklad", account],
    queryFn: ({ signal }) =>
      api<CredentialStatus>(
        `/api/integrations/moysklad/credentials?${query}`,
        signal,
      ),
    enabled: !!account,
    retry: false,
  });
  const connection = useQuery({
    queryKey: ["integrations", tenant, "moysklad", account],
    queryFn: ({ signal }) =>
      api<{ providers: MoyProvider[] }>(
        `/api/integrations?${params({ tenant, moysklad_account: account })}`,
        signal,
      ),
    enabled: !!account,
    retry: false,
  });
  const history = useQuery({
    queryKey: ["import-history", tenant, "moysklad", account, 0],
    queryFn: ({ signal }) =>
      api<{ items: ImportOperation[]; has_next: boolean }>(
        `/api/imports?${params({ tenant, moysklad_account: account, limit: 10, offset: 0 })}`,
        signal,
      ),
    enabled: !!account,
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.items.some((op) => ["QUEUED", "RUNNING"].includes(op.state))
        ? 2000
        : false,
  });
  const refresh = async () => {
    await Promise.all(
      [
        "moy-settings",
        "integrations",
        "credentials",
        "accounts",
        "diagnostics",
      ].map((key) => client.invalidateQueries({ queryKey: [key] })),
    );
  };
  return {
    settings,
    credential,
    connection,
    history,
    provider: connection.data?.providers.find(
      (p) => p.provider === "moysklad" && p.account === account,
    ),
    refresh,
  };
}
export function useSettingsDraft(
  tenant: string,
  account: string,
  original: Settings | undefined,
) {
  const client = useQueryClient();
  const [draft, setDraft] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false),
    [message, setMessage] = useState(""),
    [unconfirmed, setUnconfirmed] = useState(false);
  const lock = useRef(false);
  const values = draft || original;
  const dirty =
    !!draft &&
    !!original &&
    (draft.name !== original.name ||
      draft.description !== original.description);
  const valid =
    !!values?.name.trim() &&
    Array.from(values.name.trim()).length <= 120 &&
    Array.from(values.description.trim()).length <= 1000 &&
    !Array.from(values.name + values.description).some((char) => {
      const code = char.charCodeAt(0);
      return code < 32 || (code >= 127 && code <= 159);
    });
  const edit = (field: "name" | "description", value: string) => {
    if (original && !busy && !unconfirmed) {
      setDraft({ ...(draft || original), [field]: value });
      setMessage("");
    }
  };
  async function save() {
    if (!values || !dirty || !valid || lock.current || unconfirmed) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    const input = {
      ...values,
      name: values.name.trim(),
      description: values.description.trim(),
    };
    try {
      const saved = await post<Settings>(
        "/api/integrations/moysklad/settings",
        { tenant, account, ...input },
        AbortSignal.timeout(15000),
      );
      client.setQueryData(["moy-settings", tenant, account], saved);
      setDraft(null);
      await client.invalidateQueries({ queryKey: ["accounts", tenant] });
      setMessage("Изменения сохранены");
    } catch (error) {
      if (error instanceof ApiError && error.status < 500)
        setMessage(
          error.code === "SETTINGS_VERSION_CONFLICT"
            ? "Настройки изменились в другой вкладке. Скопируйте свой текст и загрузите актуальные данные."
            : "Не удалось сохранить настройки. Проверьте данные и доступ к аккаунту.",
        );
      else {
        setUnconfirmed(true);
        setMessage(
          "Результат сохранения неизвестен. Проверьте сохранение перед новым изменением.",
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function inspect() {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    try {
      const saved = await api<Settings>(
        `/api/integrations/moysklad/settings?${params({ tenant, account })}`,
      );
      client.setQueryData(["moy-settings", tenant, account], saved);
      if (
        values &&
        saved.name === values.name.trim() &&
        saved.description === values.description.trim()
      ) {
        setDraft(null);
        setMessage("Сохранение подтверждено");
      } else {
        setDraft(values ? { ...values, revision: saved.revision } : null);
        setMessage(
          "Текущее состояние загружено. Ваш текст сохранён в форме; проверьте его перед сохранением.",
        );
      }
      setUnconfirmed(false);
    } catch {
      setMessage("Не удалось прочитать настройки. Повторите проверку позже.");
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return {
    values,
    dirty,
    valid,
    busy,
    message,
    unconfirmed,
    edit,
    save,
    inspect,
    reset: () => {
      setDraft(null);
      setMessage("");
    },
  };
}
export function useSettingsLeaveGuard(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return;
    const unload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    const click = (event: MouseEvent) => {
      const anchor = (event.target as Element)?.closest?.("a[href]");
      if (
        !(anchor instanceof HTMLAnchorElement) ||
        anchor.target === "_blank" ||
        (anchor.pathname === location.pathname &&
          anchor.search === location.search)
      )
        return;
      if (
        !window.confirm("Есть несохранённые изменения. Покинуть настройки?")
      ) {
        event.preventDefault();
        event.stopPropagation();
      }
    };
    window.addEventListener("beforeunload", unload);
    document.addEventListener("click", click, true);
    return () => {
      window.removeEventListener("beforeunload", unload);
      document.removeEventListener("click", click, true);
    };
  }, [dirty]);
}
