import {
  knownOrderField,
  orderFieldText,
  orderDate,
  type OrderSplitResponsePosting,
  type OrderWork,
} from "./OrdersTypes";

export function OrdersSplitEvidence({
  receipt,
  state,
}: {
  receipt: OrderWork["receipt"];
  state?: string;
}) {
  const numbers = receipt?.posting_numbers ?? [];
  if (receipt?.unassembled_split)
    return <UnassembledSplitEvidence receipt={receipt} state={state} />;
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
  state,
}: {
  receipt: OrderWork["receipt"];
  state?: string;
}) {
  const facts = receipt.unassembled_split!;
  const postings = knownOrderField(facts.postings);
  const verified =
    (state ?? receipt.state) === "RECONCILED" &&
    !!receipt.posting_numbers?.length;
  return (
    <details>
      <summary>Ответ Ozon на разделение без сборки</summary>
      <p className="orders-note">
        Это сведения из ответа API. Сборка и передача в доставку не выполнялись.
        Синхронизация подтверждается только после отдельной проверки
        фактического состава.
      </p>
      <UnassembledVerification receipt={receipt} verified={verified} />
      <UnassembledParentEvidence parent={facts.parent} />
      {postings ? (
        <ul>
          {postings.map((posting, index) => (
            <UnassembledPostingEvidence
              key={index}
              posting={posting}
              receipt={receipt}
              verified={verified}
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
  verified,
}: {
  posting: OrderSplitResponsePosting;
  receipt: OrderWork["receipt"];
  verified: boolean;
}) {
  const confirmed =
    verified &&
    !!receipt.posting_numbers?.includes(
      knownOrderField(posting.posting_number) ?? "",
    );
  return (
    <li>
      <strong>{orderFieldText(posting.posting_number)}</strong>
      <p>Состав в ответе Ozon:</p>
      <SplitResponseContents posting={posting} />
      <span>
        {confirmed
          ? "Состав подтверждён отдельной проверкой"
          : "Ожидаем подтверждения состава"}
      </span>
    </li>
  );
}

function UnassembledVerification({
  receipt,
  verified,
}: {
  receipt: OrderWork["receipt"];
  verified: boolean;
}) {
  const count = receipt.split_read_count;
  const countKnown =
    typeof count === "number" &&
    Number.isSafeInteger(count) &&
    count >= 0 &&
    count <= 1001;
  return (
    <>
      {countKnown && (
        <p>
          Сохранено результатов проверки: {count}. Это число само по себе не
          подтверждает полный состав.
        </p>
      )}
      {verified ? (
        <>
          <p>Полный состав подтверждён отдельной проверкой всех отправлений.</p>
          <details>
            <summary>
              Подтверждённые отправления ({receipt.posting_numbers!.length})
            </summary>
            <ul>
              {receipt.posting_numbers!.map((number) => (
                <li key={number}>{number}</li>
              ))}
            </ul>
          </details>
        </>
      ) : (
        <p>Полный состав ещё не подтверждён.</p>
      )}
    </>
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
