import {
  knownOrderField,
  orderFieldText,
  orderDate,
  type OrderSplitResponsePosting,
  type OrderWork,
} from "./OrdersTypes";

export function OrdersSplitEvidence({
  receipt,
}: {
  receipt: OrderWork["receipt"];
}) {
  const numbers = receipt?.posting_numbers ?? [];
  if (receipt?.unassembled_split)
    return <UnassembledSplitEvidence receipt={receipt} />;
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

function UnassembledSplitEvidence({
  receipt,
}: {
  receipt: OrderWork["receipt"];
}) {
  const facts = receipt.unassembled_split!;
  const postings = knownOrderField(facts.postings);
  return (
    <details>
      <summary>Ответ Ozon на разделение без сборки</summary>
      <p className="orders-note">
        Это сведения из ответа API. Сборка и передача в доставку не выполнялись.
        Синхронизация подтверждается только после отдельной проверки
        фактического состава.
      </p>
      <UnassembledParentEvidence parent={facts.parent} />
      {postings ? (
        <ul>
          {postings.map((posting, index) => (
            <UnassembledPostingEvidence
              key={index}
              posting={posting}
              receipt={receipt}
            />
          ))}
        </ul>
      ) : (
        <p>Новые отправления: {orderFieldText(facts.postings)}</p>
      )}
    </details>
  );
}

function SplitResponseContents({
  posting,
}: {
  posting: OrderSplitResponsePosting;
}) {
  const products = knownOrderField(posting.products);
  if (!products) return <p>Состав: {orderFieldText(posting.products)}</p>;
  return (
    <ul>
      {products.map((product, index) => (
        <li key={index}>
          SKU {product.sku}: {product.quantity}
        </li>
      ))}
    </ul>
  );
}

function UnassembledPostingEvidence({
  posting,
  receipt,
}: {
  posting: OrderSplitResponsePosting;
  receipt: OrderWork["receipt"];
}) {
  const number = knownOrderField(posting.posting_number);
  const observed = receipt.observed_postings?.find(
    (item) => item.posting_number === number,
  );
  return (
    <li>
      <strong>{orderFieldText(posting.posting_number)}</strong>
      <SplitResponseContents posting={posting} />
      <span>
        {observed
          ? `Состав проверен: ${orderDate(observed.evidence.checked_at || observed.evidence.observed_at)}`
          : "Ожидаем подтверждения состава"}
      </span>
    </li>
  );
}

function UnassembledParentEvidence({
  parent,
}: {
  parent: NonNullable<OrderWork["receipt"]["unassembled_split"]>["parent"];
}) {
  const posting = knownOrderField(parent);
  return (
    <>
      <p>
        Исходное отправление:{" "}
        {posting
          ? orderFieldText(posting.posting_number)
          : orderFieldText(parent)}
      </p>
      <p>
        Эта информация не доказывает отмену или исчезновение исходного
        отправления.
      </p>
      {posting && <SplitResponseContents posting={posting} />}
    </>
  );
}
