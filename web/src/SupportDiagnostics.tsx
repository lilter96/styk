import { useState } from "react";
import { Link } from "react-router-dom";
import { IntegrationDialog } from "./IntegrationDialog";
import type { ImportOperation } from "./api";
export function SupportDiagnostics({
  operation,
  onClose,
}: {
  operation?: ImportOperation;
  onClose: () => void;
}) {
  const [message, setMessage] = useState("");
  const facts = operation
    ? [
        [
          "Событие",
          `Обновление ${operation.provider === "moysklad" ? "МойСклад" : "Ozon"}`,
        ],
        ["Результат", operation.state],
        ["Этап", operation.phase],
        ["Итог", operation.outcome || "—"],
        ["Аккаунт", operation.account],
        ["Пространство", operation.tenant],
        ["Операция", operation.id],
        [
          "Пакет",
          operation.batch_id === null ? "—" : String(operation.batch_id),
        ],
        [
          "Запросов",
          `${operation.counts.requests} из ${operation.limits.requests}`,
        ],
        [
          "Длительность попытки",
          `${(operation.elapsed_ms / 1000).toLocaleString("ru-RU")} с`,
        ],
        ["Время", new Date(operation.created_at).toLocaleString("ru-RU")],
      ]
    : [];
  return (
    <IntegrationDialog title="Диагностика" onClose={onClose}>
      <div className="provider-settings-body">
        <p>
          Для службы поддержки. Скопируйте данные выбранного события и отправьте
          специалисту.
        </p>
        {facts.length ? (
          <dl className="support-facts">
            {facts.map(([label, value]) => (
              <div key={label} style={{ display: "contents" }}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        ) : (
          <p>
            Выберите событие после первой загрузки данных. Подробная диагностика
            доступна в разделе ниже.
          </p>
        )}
        <button
          disabled={!facts.length}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(
                facts.map(([key, value]) => `${key}: ${value}`).join("\n"),
              );
              setMessage("Скопировано");
            } catch {
              setMessage("Выделите и скопируйте данные вручную.");
            }
          }}
        >
          Скопировать для поддержки
        </button>
        {message && <p role="status">{message}</p>}
        <p>Ключи доступа сюда не попадают.</p>
        <Link to="/diagnostics">Открыть полную диагностику</Link>
      </div>
    </IntegrationDialog>
  );
}
