import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params } from "./api";
import { scopeBody, type Scope } from "./CatalogSyncTypes";
import {
  matchingCriteria,
  type MatchingPolicy,
  type MatchingCriterion,
} from "./ProductMatchingTypes";
export function ProductMatchingAutomation({ scope }: { scope: Scope }) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ["product-matching-settings", scope],
    queryFn: ({ signal }) =>
      api<MatchingPolicy>(
        "/api/product-matching/settings?" + params(scopeBody(scope)),
        signal,
      ),
    retry: false,
  });
  const [edited, setEdited] = useState<MatchingPolicy | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const policy = edited || query.data;
  const changed =
    !!edited && JSON.stringify(edited) !== JSON.stringify(query.data);
  async function save() {
    if (!policy || busy) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await post<MatchingPolicy>(
        "/api/product-matching/settings",
        { ...scopeBody(scope), policy },
        AbortSignal.timeout(30000),
      );
      client.setQueryData(["product-matching-settings", scope], result);
      setEdited(null);
      setMessage("Автоматизация сохранена");
    } catch {
      setMessage("Не удалось сохранить автоматизацию. Повторите сохранение.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="automatch-automation">
      <summary>Сопоставлять товары после новых загрузок</summary>
      <div className="automatch-automation-body">
        <p>
          Отдельное постоянное правило для этой пары аккаунтов. Разовый запуск
          выше его не изменяет.
        </p>
        {query.isLoading && <p role="status">Загружаем правило…</p>}
        {query.isError && (
          <button className="secondary" onClick={() => void query.refetch()}>
            Повторить загрузку правила
          </button>
        )}
        {policy && (
          <>
            <label className="automatch-checkbox">
              <input
                type="checkbox"
                checked={policy.after_import}
                disabled={busy}
                onChange={(e) =>
                  setEdited({
                    ...policy,
                    after_import: e.target.checked,
                    auto_confirm: e.target.checked,
                    exact_sku: true,
                  })
                }
              />
              <span>
                Автоматически сопоставлять точные пары после новых полных
                загрузок
              </span>
            </label>
            <label className="automatch-automation-criterion">
              Критерий для новых загрузок
              <select
                value={policy.criterion || "article_or_code"}
                disabled={busy}
                onChange={(e) =>
                  setEdited({
                    ...policy,
                    criterion: e.target.value as MatchingCriterion,
                  })
                }
              >
                {matchingCriteria.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.title}
                  </option>
                ))}
              </select>
            </label>
            <div className="automatch-actions">
              <button disabled={busy || !changed} onClick={() => void save()}>
                {busy ? "Сохраняем…" : "Сохранить автоматизацию"}
              </button>
              {message && <span role="status">{message}</span>}
            </div>
          </>
        )}
      </div>
    </details>
  );
}
