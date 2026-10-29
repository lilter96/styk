import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post } from "./api";

type Member = { user_id: string; email: string; role: string };
type Event = { id: number; action: string; created_at: string };
const roleNames: Record<string, string> = {
  OWNER: "Владелец",
  ADMIN: "Администратор",
  OPERATOR: "Оператор",
  VIEWER: "Просмотр",
};

export function WorkspaceSettings({
  workspace,
  role,
  switchTo,
}: {
  workspace: string;
  role: string;
  switchTo: (id: string) => void;
}) {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [memberRole, setMemberRole] = useState("OPERATOR");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const admin = role === "OWNER" || role === "ADMIN";
  const path = `/api/workspaces/${encodeURIComponent(workspace)}/members`;
  const members = useQuery({
    queryKey: ["workspace-members", workspace],
    queryFn: ({ signal }) => api<{ members: Member[] }>(path, signal),
    enabled: open && admin && !!workspace,
  });
  const audit = useQuery({
    queryKey: ["workspace-audit", workspace],
    queryFn: ({ signal }) =>
      api<{ events: Event[] }>(
        `/api/workspaces/${encodeURIComponent(workspace)}/audit`,
        signal,
      ),
    enabled: open && admin && !!workspace,
  });

  async function create(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const result = await post<{ id: string }>("/api/workspaces", { name });
      switchTo(result.id);
    } catch {
      setMessage("Не удалось создать пространство. Проверьте название.");
    } finally {
      setBusy(false);
    }
  }
  async function updateMember(address: string, nextRole: string) {
    setBusy(true);
    setMessage("");
    try {
      await post(path, { email: address, role: nextRole });
      setEmail("");
      setMessage(nextRole ? "Доступ обновлён." : "Доступ отозван.");
      await Promise.all([
        members.refetch(),
        client.invalidateQueries({ queryKey: ["session"] }),
      ]);
    } catch {
      setMessage(
        "Не удалось изменить доступ. Пользователь должен подтвердить почту; в пространстве должен остаться владелец.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <details
      className="merchant-workspace-settings"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>Настройки пространства</summary>
      <p>Ваша роль: {roleNames[role] || "Нет доступа"}.</p>
      {admin && (
        <>
          <h3>Участники</h3>
          {members.isLoading && <p>Загружаем участников…</p>}
          {members.isError && (
            <p role="alert">Не удалось загрузить участников.</p>
          )}
          {members.data?.members.map((member) => (
            <div className="merchant-member" key={member.user_id}>
              <span>
                {member.email}
                <small>{roleNames[member.role] || member.role}</small>
              </span>
              <button
                type="button"
                className="text-action"
                disabled={busy}
                onClick={() => void updateMember(member.email, "")}
              >
                Отозвать
              </button>
            </div>
          ))}
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void updateMember(email, memberRole);
            }}
          >
            <label>
              Почта участника
              <input
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
            </label>
            <label>
              Роль
              <select
                value={memberRole}
                onChange={(event) => setMemberRole(event.target.value)}
              >
                <option value="OPERATOR">Оператор</option>
                <option value="VIEWER">Просмотр</option>
                <option value="ADMIN">Администратор</option>
                {role === "OWNER" && <option value="OWNER">Владелец</option>}
              </select>
            </label>
            <small>
              Участник должен сначала зарегистрироваться и подтвердить почту.
            </small>
            <button type="submit" className="secondary" disabled={busy}>
              Сохранить доступ
            </button>
          </form>
          <h3>Последние действия</h3>
          {audit.isError && (
            <p role="alert">Не удалось загрузить историю действий.</p>
          )}
          {audit.data?.events.slice(0, 5).map((event) => (
            <p key={event.id}>
              {event.action === "WORKSPACE_CREATED"
                ? "Создано пространство"
                : event.action === "MEMBER_ROLE_CHANGED"
                  ? "Изменён доступ участника"
                  : event.action === "MEMBER_REMOVED"
                    ? "Отозван доступ участника"
                    : event.action === "LOCAL_BOOTSTRAP_CLAIM"
                      ? "Подключены прежние данные"
                      : "Запрошено изменение"}{" "}
              <time dateTime={event.created_at}>
                {new Date(event.created_at).toLocaleString("ru-RU")}
              </time>
            </p>
          ))}
        </>
      )}
      <h3>Новое пространство</h3>
      <form onSubmit={(event) => void create(event)}>
        <label>
          Название магазина
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={120}
            required
          />
        </label>
        <button type="submit" className="secondary" disabled={busy}>
          Создать пространство
        </button>
      </form>
      {message && <p role="status">{message}</p>}
    </details>
  );
}
