import { orderDate, type OrderWork } from "./OrdersTypes";

export function OrdersSplitEvidence({
  receipt,
}: {
  receipt: OrderWork["receipt"];
}) {
  const numbers = receipt?.posting_numbers ?? [];
  if (!numbers.length) return null;
  return (
    <details>
      <summary>Посылки после разделения ({numbers.length})</summary>
      <p className="orders-note">
        STYK проверяет состав каждой посылки. Ответ на разделение ещё не
        подтверждает сборку.
      </p>
      <ul>
        {numbers.map((number) => {
          const observation = receipt.observed_postings?.find(
            (posting) => posting.posting_number === number,
          );
          return (
            <li key={number}>
              <strong>{number}</strong>
              {" — "}
              <span>
                {observation
                  ? `Состав проверен: ${orderDate(observation.evidence.checked_at || observation.evidence.observed_at)}`
                  : "Ожидаем подтверждения состава"}
              </span>
            </li>
          );
        })}
      </ul>
    </details>
  );
}
