import { useId, useState, type ReactNode } from "react";
import { IntegrationDialog } from "./IntegrationDialog";
import { Icon } from "./UI";
import { OrdersTabs } from "./OrdersTabs";
import { OrdersActionEligibility } from "./OrdersActionEligibility";
import { OrdersActions } from "./OrdersActions";
import { OrdersNativeDocuments } from "./OrdersNativeDocuments";
import {
  OrdersComposition,
  type OrderCompositionProposal,
} from "./OrdersComposition";
import { OrdersRelatedPostings } from "./OrdersRelatedPostings";
import { OrdersOperations } from "./OrdersOperations";
import {
  orderQuantity,
  postingTotal,
  PostingStatus,
  PostingTargetStatus,
  displayOrderMoney,
} from "./OrdersPresentation";
import type { OrderTargetState } from "./OrdersTargetState";
import type { OrderNativeProposal } from "./OrdersNativeTypes";
import {
  knownOrderField,
  orderDate,
  orderFieldText,
  orderStateLabels,
  MoyOrderLink,
  type OrderScope,
  type OrderPosting,
  type OrderActionProposal,
} from "./OrdersTypes";

export type OrderDetailActions = {
  onActionPreview?: (proposal: OrderActionProposal) => void;
  onNativePreview?: (proposal: OrderNativeProposal) => void;
  onCompositionPreview?: (proposal: OrderCompositionProposal) => void;
  onActionDraftChange?: () => void;
  onRecoveryAuthorization?: (
    posting: string,
    until: string | undefined,
  ) => void;
};
const detailTabs = [
  { value: "overview", label: "Обзор" },
  { value: "products", label: "Товары" },
  { value: "actions", label: "Действия" },
  { value: "history", label: "История" },
] as const;
type DetailTab = (typeof detailTabs)[number]["value"];

type Props = OrderDetailActions & {
  scope: OrderScope;
  posting: OrderPosting;
  local?: OrderTargetState;
  link?: string;
  disabled: boolean;
  locked: boolean;
  freshnessSeconds: number;
  actionReset: number;
  onClose: () => void;
  onFind: (number: string) => void;
  preview?: ReactNode;
  notice?: ReactNode;
  onDismissPreview?: () => void;
};

export function OrderDetailDrawer(props: Props) {
  const { posting, local, locked, onClose, preview } = props;
  const [tab, setTab] = useState<DetailTab>("overview");
  const [actionsVisited, setActionsVisited] = useState(false);
  const [historyVisited, setHistoryVisited] = useState(false);
  const id = useId();
  function navigate(next: DetailTab) {
    setTab(next);
    if (next === "actions") setActionsVisited(true);
    if (next === "history") setHistoryVisited(true);
  }
  return (
    <IntegrationDialog
      title={posting.posting_number}
      onClose={onClose}
      closeLabel="Закрыть отправление"
      className="orders-drawer"
      initialFocus="[role=tab][aria-selected=true]"
    >
      <div className="orders-detail-context">
        <span>Заказ {orderFieldText(posting.order_number)}</span>
        <PostingStatus posting={posting} />
      </div>
      {props.notice}
      {preview ? (
        <div className="orders-detail-preview">
          <button
            type="button"
            className="text-action"
            onClick={props.onDismissPreview}
          >
            <Icon name="chevron-left" size="button" />К отправлению
          </button>
          {preview}
        </div>
      ) : null}
      <div hidden={!!preview}>
        <OrdersTabs
          id={id}
          label="Отправление"
          tabs={detailTabs}
          value={tab}
          onChange={navigate}
        />
        <div
          role="tabpanel"
          id={`${id}-overview-panel`}
          aria-labelledby={`${id}-overview-tab`}
          hidden={tab !== "overview"}
          className="orders-detail-panel"
        >
          <OrderDetailOverview {...props} />
          <div className="orders-products-summary">
            <div className="orders-section-heading">
              <h3>Состав отправления</h3>
              <button
                type="button"
                className="text-action"
                onClick={() => navigate("products")}
              >
                Все товары <Icon name="arrow" size="button" />
              </button>
            </div>
            <OrderDetailProducts posting={posting} compact />
          </div>
          <h3>Действия с отправлением</h3>
          <button
            type="button"
            className="orders-action-entry secondary"
            onClick={() => navigate("actions")}
          >
            <Icon name="products" />
            <span>
              <strong>Операции Ozon и МойСклад</strong>
              <small>Сборка, документы, отмена и состав заказа</small>
            </span>
            <Icon name="chevron-right" size="button" />
          </button>
        </div>
        <div
          role="tabpanel"
          id={`${id}-products-panel`}
          aria-labelledby={`${id}-products-tab`}
          hidden={tab !== "products"}
          className="orders-detail-panel"
        >
          <h3>Товары отправления</h3>
          <OrderDetailProducts posting={posting} />
          <p className="orders-note">
            {posting.lines?.length ?? 0} позиций ·{" "}
            {orderQuantity(posting.lines ?? [])} шт.
          </p>
        </div>
        <div
          role="tabpanel"
          id={`${id}-actions-panel`}
          aria-labelledby={`${id}-actions-tab`}
          hidden={tab !== "actions"}
          className="orders-detail-panel orders-detail-actions"
        >
          <h3>Действия с отправлением</h3>
          <p className="orders-note">
            Выберите операцию и заполните параметры. Затем проверьте
            предпросмотр и подтвердите изменения.
          </p>
          {locked && (
            <p className="orders-warning">
              Результат подтверждения неизвестен. Повторная отправка запрещена
              до сверки. Восстановление доступно во вкладке «История».
            </p>
          )}
          {actionsVisited && <OrderDetailActionForms {...props} />}
        </div>
        <div
          role="tabpanel"
          id={`${id}-history-panel`}
          aria-labelledby={`${id}-history-tab`}
          hidden={tab !== "history"}
          className="orders-detail-panel"
        >
          {historyVisited && (
            <OrdersOperations
              scope={props.scope}
              postingNumber={posting.posting_number}
              onRecoveryAuthorization={props.onRecoveryAuthorization}
            />
          )}
        </div>
      </div>
      <footer className="orders-drawer-footer">
        <p>
          Сначала предпросмотр —<br />
          затем подтверждение изменений
        </p>
        <button type="button" className="secondary" onClick={onClose}>
          Закрыть
        </button>
      </footer>
      {local?.unresolved && (
        <span className="sr-only">Повторная отправка заблокирована</span>
      )}
    </IntegrationDialog>
  );
}

function OrderDetailActionForms(props: Props) {
  const { posting, actionReset, disabled, locked } = props;
  const changed = props.onActionDraftChange ?? (() => {});
  const revision = `${posting.evidence.revision}:${actionReset}`;
  return (
    <>
      {props.onActionPreview && (
        <OrdersActions
          key={revision}
          scope={props.scope}
          posting={posting}
          disabled={disabled || locked}
          onPreview={props.onActionPreview}
          onDraftChange={changed}
        />
      )}
      {props.onNativePreview && (
        <OrdersNativeDocuments
          key={`native:${revision}`}
          scope={props.scope}
          posting={posting}
          disabled={disabled || locked}
          onPreview={props.onNativePreview}
          onDraftChange={changed}
        />
      )}
      {props.onCompositionPreview && (
        <OrdersComposition
          key={`composition:${revision}`}
          postingNumber={posting.posting_number}
          disabled={disabled || locked}
          onPreview={props.onCompositionPreview}
          onDraftChange={changed}
        />
      )}
      <details>
        <summary>Условия для действий в Ozon</summary>
        <OrdersActionEligibility
          posting={posting}
          freshnessSeconds={props.freshnessSeconds}
        />
      </details>
    </>
  );
}

function OrderDetailOverview({
  posting,
  local,
  link,
  locked,
  onFind,
  freshnessSeconds,
}: Props) {
  const parent = knownOrderField(posting.parent_posting_number);
  const checked = Date.parse(posting.evidence.checked_at ?? "");
  const stale =
    !Number.isFinite(checked) ||
    checked > Date.now() ||
    checked + freshnessSeconds * 1000 <= Date.now();
  const facts = [
    ["Схема", orderFieldText(posting.scheme)],
    ["Склад Ozon", orderFieldText(posting.warehouse_id)],
    ["Сумма Ozon", postingTotal(posting)],
    [
      "Состав",
      `${posting.lines?.length ?? 0} позиций · ${orderQuantity(posting.lines ?? [])} шт.`,
    ],
    ["Проверено STYK", orderDate(posting.evidence.checked_at)],
  ];
  return (
    <>
      <dl className="orders-overview-facts">
        {facts.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <div
        className={`orders-link-state ${locked || local?.source_changed || ["UNKNOWN", "BLOCKED", "REJECTED"].includes(local?.state ?? "") ? "needs-attention" : ""}`}
      >
        <PostingTargetStatus local={local} locked={locked} />
        {link && <MoyOrderLink id={link} />}
      </div>
      <details className="orders-source-details">
        <summary>Сведения о связи и источнике</summary>
        <div className="orders-detail-body">
          <dl className="orders-overview-facts">
            <div>
              <dt>Получено из Ozon</dt>
              <dd>{orderDate(posting.evidence.observed_at)}</dd>
            </div>
            <div>
              <dt>Обновлено в источнике</dt>
              <dd>
                {orderFieldText(
                  posting.evidence.provider_updated_at,
                  orderDate,
                )}
              </dd>
            </div>
            <div>
              <dt>ID заказа Ozon</dt>
              <dd>{posting.commercial_order_id}</dd>
            </div>
          </dl>
          {!link && <MoyOrderLink id={undefined} />}
          {local && (
            <p className="orders-note">
              Последняя операция:{" "}
              {local.has_operation
                ? (orderStateLabels[local.state ?? ""] ??
                  "Состояние требует проверки")
                : "Передач ещё не было"}
              . Состояние STYK проверено: {orderDate(local.checked_at)}. Это не
              новое чтение Ozon.
            </p>
          )}
        </div>
      </details>
      {locked && (
        <p className="orders-warning">
          Есть неизвестный результат. Откройте историю этого отправления для
          проверки; повторная отправка заблокирована.
        </p>
      )}
      {local?.source_changed && (
        <p className="orders-warning">
          После последней операции данные источника изменились. Нужен новый
          предпросмотр.
        </p>
      )}
      {local?.state === "UNKNOWN" && !local.unresolved && (
        <p className="orders-warning">
          Неизвестный исход сохранён в истории. Активная блокировка не найдена;
          это не подтверждение исполнения. Новые действия требуют отдельной
          проверки.
        </p>
      )}
      {stale && (
        <p className="orders-warning">
          Данные Ozon требуют новой проверки источника. Сохранённые сведения не
          подтверждают текущие условия.
        </p>
      )}
      {knownOrderField(posting.substatus) !== undefined && (
        <p className="orders-note">
          Подстатус: {orderFieldText(posting.substatus)}
        </p>
      )}
      {posting.drift?.length > 0 && (
        <details className="orders-source-details">
          <summary>
            Нужна проверка совместимости данных Ozon · замечаний:{" "}
            {posting.drift.length}
          </summary>
          {posting.drift.map((item, index) => (
            <p className="orders-warning" key={index}>
              Изменение схемы источника: <code>{item.path}</code> ·{" "}
              <code>{item.code}</code>. Нужна проверка совместимости.
            </p>
          ))}
        </details>
      )}
      <OrdersRelatedPostings
        group={posting.related_postings}
        current={posting.posting_number}
        onFind={onFind}
      />
      {(parent || posting.child_posting_numbers?.length > 0) && (
        <div className="orders-split">
          {parent && (
            <button
              type="button"
              className="text-action"
              onClick={() => onFind(parent)}
            >
              Родительское отправление {parent}
            </button>
          )}
          {(posting.child_posting_numbers ?? []).map((number) => (
            <button
              type="button"
              className="text-action"
              key={number}
              onClick={() => onFind(number)}
            >
              Дочернее отправление {number}
            </button>
          ))}
        </div>
      )}
    </>
  );
}

function OrderDetailProducts({
  posting,
  compact = false,
}: {
  posting: OrderPosting;
  compact?: boolean;
}) {
  const lines = posting.lines ?? [];
  return (
    <div className="orders-product-list">
      {!lines.length && (
        <p className="orders-empty">Состав отправления не получен.</p>
      )}
      {(compact ? lines.slice(0, 2) : lines).map((line, index) => (
        <div
          className="orders-product"
          key={`${JSON.stringify(line.identity)}:${index}`}
        >
          <span
            className="orders-product-placeholder"
            aria-label="Нет изображения"
          >
            <Icon name="products" />
          </span>
          <div>
            <strong>{orderFieldText(line.name)}</strong>
            <p>
              SKU {line.identity.sku ?? "неизвестен"} ·{" "}
              {line.quantity || "Неизвестно"} шт. ·{" "}
              {orderFieldText(line.price, displayOrderMoney)}
            </p>
            {!compact && (
              <>
                <p>Артикул {line.identity.offer_id ?? "неизвестен"}</p>
                <p>Отменено: {orderFieldText(line.cancelled_quantity)}</p>
                {line.identity.provider_line_id && (
                  <p>ID позиции: {line.identity.provider_line_id}</p>
                )}
              </>
            )}
          </div>
        </div>
      ))}
      {compact && lines.length > 2 && (
        <p className="orders-note">Ещё позиций: {lines.length - 2}</p>
      )}
    </div>
  );
}
