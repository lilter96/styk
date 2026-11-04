import { useState } from "react";
import { knownOrderField, type RelatedPostingGroup } from "./OrdersTypes";

export function OrdersRelatedPostings({
  group,
  current,
  onFind,
}: {
  group?: RelatedPostingGroup;
  current: string;
  onFind: (number: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  if (!group) return null;
  const observed = knownOrderField(group.posting_numbers);
  const numbers = Array.isArray(observed)
    ? observed.filter((number) => number !== current)
    : [];
  const empty = group.posting_numbers?.presence === "empty";
  const visible = expanded ? numbers : numbers.slice(0, 10);
  return (
    <details>
      <summary>Связанные отправления сборки</summary>
      {numbers.length ? (
        <div className="orders-lineage">
          {visible.map((number) => (
            <button
              type="button"
              className="secondary"
              key={number}
              onClick={() => onFind(number)}
            >
              Найти связанное отправление {number}
            </button>
          ))}
          {numbers.length > 10 && (
            <button
              type="button"
              className="secondary"
              aria-expanded={expanded}
              onClick={() => setExpanded(!expanded)}
            >
              {expanded
                ? "Свернуть список связей"
                : `Показать ещё ${numbers.length - 10}`}
            </button>
          )}
        </div>
      ) : (
        <p className="orders-note">
          {empty
            ? "Ozon передал пустой список связей."
            : Array.isArray(observed)
              ? "Других отправлений в полученном списке нет."
              : "Ozon не передал список связанных отправлений."}
        </p>
      )}
      <p className="orders-note">
        Связь сборки не означает, что резерв уже перенесён.
      </p>
    </details>
  );
}
