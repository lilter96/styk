import { useEffect, useState } from "react";
import { BrandLogo } from "./BrandLogo";
import { CatalogSyncValue } from "./CatalogSyncValue";
import {
  displayValue,
  fieldLabel,
  issueLabel,
  productName,
  providerLabel,
  type CatalogPreview,
  type Decision,
} from "./CatalogSyncTypes";

function canApprove(decision: Decision) {
  const values = decision.commands.flatMap((command) =>
    Object.values({ ...command.values, ...command.extensions }),
  );
  return (
    decision.ready &&
    decision.validation.length === 0 &&
    decision.plan.conflicts.length === 0 &&
    decision.commands.length > 0 &&
    values.length > 0 &&
    values.every(
      (value) =>
        value.state === "clear" ||
        (value.state === "known" && value.data != null),
    )
  );
}

function DecisionDiff({ decision }: { decision: Decision }) {
  const changes = decision.plan.changes.length
    ? decision.plan.changes
    : decision.commands
        .filter((command) => command.action === "create")
        .flatMap((command) =>
          Object.entries({ ...command.values, ...command.extensions }).map(
            ([field, after]) => ({
              field,
              source: decision.source.identity,
              target: command.target,
              before: { state: "absent" as const },
              after,
            }),
          ),
        );
  return (
    <>
      {changes.length > 0 && (
        <div
          className="catalog-table-scroll"
          tabIndex={0}
          aria-label={`Изменения товара ${productName(decision.source)}`}
        >
          <table className="catalog-table catalog-diff">
            <thead>
              <tr>
                <th scope="col">Поле и направление</th>
                <th scope="col">Сейчас у получателя</th>
                <th scope="col">После синхронизации</th>
              </tr>
            </thead>
            <tbody>
              {changes.map((change, index) => (
                <tr key={`${change.field}:${index}`}>
                  <th scope="row">
                    {fieldLabel(change.field)}
                    <small>
                      {providerLabel(change.source.account.provider)} →{" "}
                      {providerLabel(change.target.account.provider)}
                    </small>
                  </th>
                  <td>
                    <CatalogSyncValue
                      field={change.field}
                      value={change.before}
                    />
                  </td>
                  <td>
                    <CatalogSyncValue
                      field={change.field}
                      value={change.after}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {decision.plan.conflicts.map((conflict, index) => (
        <div
          className="catalog-conflict"
          role="alert"
          key={`${conflict.field}:${index}`}
        >
          <strong>{fieldLabel(conflict.field)}: конфликт</strong>
          <p>{issueLabel(conflict.reason)}</p>
          <dl>
            <div>
              <dt>В источнике</dt>
              <dd>{displayValue(conflict.source_value)}</dd>
            </div>
            <div>
              <dt>Во втором сервисе</dt>
              <dd>{displayValue(conflict.target_value)}</dd>
            </div>
          </dl>
        </div>
      ))}
      {decision.validation.map((issue, index) => (
        <p
          className="catalog-error"
          role="alert"
          key={`${issue.field}:${index}`}
        >
          {fieldLabel(issue.field)}: {issueLabel(issue.code)}
        </p>
      ))}
      {decision.plan.skipped.length > 0 && (
        <details>
          <summary>Пропущенные поля: {decision.plan.skipped.length}</summary>
          <ul>
            {decision.plan.skipped.map((skip, index) => (
              <li key={`${skip.field}:${index}`}>
                {fieldLabel(skip.field)}: {issueLabel(skip.reason)}
              </li>
            ))}
          </ul>
        </details>
      )}
      {decision.plan.noop && !decision.validation.length && (
        <p>Изменений для отправки нет.</p>
      )}
      {!decision.ready &&
        !decision.validation.length &&
        !decision.plan.conflicts.length &&
        !decision.plan.noop && (
          <p className="catalog-warning">
            Товар не готов к отправке. Проверьте настройки и загруженные данные.
          </p>
        )}
    </>
  );
}

export function CatalogSyncPreview({
  preview,
  busy,
  blocked,
  onApprove,
  initialSelected = [],
}: {
  preview: CatalogPreview;
  busy: boolean;
  blocked: boolean;
  onApprove: (enrollmentIDs: string[]) => void;
  initialSelected?: string[];
}) {
  const [selected, setSelected] = useState<string[]>(initialSelected);
  const [acknowledged, setAcknowledged] = useState(false);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const expires = Date.parse(preview.expires_at);
  const expired = !Number.isFinite(expires) || expires <= now;
  const chosen = selected.filter((id) =>
    preview.decisions.some(
      (decision) => decision.enrollment_id === id && canApprove(decision),
    ),
  );
  const disabled = busy || blocked || expired;
  return (
    <section
      className="catalog-section products-preview"
      aria-labelledby="catalog-preview-title"
    >
      <h2 id="catalog-preview-title">Проверьте изменения перед отправкой</h2>
      <p>
        Подтверждается весь набор изменений выбранного товара. Для изменения
        состава полей вернитесь к настройкам и выполните новую проверку.
      </p>
      {expired ? (
        <p className="catalog-warning" role="alert">
          Срок проверки истёк. Выполните проверку заново.
        </p>
      ) : (
        <p>
          Проверка действительна до{" "}
          {new Date(preview.expires_at).toLocaleString("ru-RU")}.
        </p>
      )}
      {!preview.decisions.length && (
        <p className="catalog-empty">
          В проверке нет товаров. Выберите товары и повторите проверку.
        </p>
      )}
      <div className="products-preview-layout">
        <div className="products-preview-items">
          <div className="products-preview-summary">
            <span>
              <b>{preview.decisions.length}</b> выбраны
            </span>
            <span>
              <b>{preview.decisions.filter(canApprove).length}</b> готовы
            </span>
            <span>
              <b>
                {
                  preview.decisions.filter(
                    (d) => !canApprove(d) && !d.plan.noop,
                  ).length
                }
              </b>{" "}
              требуют проверки
            </span>
          </div>
          {preview.decisions.map((decision) => (
            <article className="catalog-decision" key={decision.enrollment_id}>
              <header>
                <span className="products-image-placeholder">
                  <BrandLogo
                    provider={decision.source.identity.account.provider}
                    decorative
                  />
                </span>
                <h3>{productName(decision.source)}</h3>
                <span
                  className={`mapping-chip ${canApprove(decision) ? "ok" : "warning"}`}
                >
                  {canApprove(decision)
                    ? "Готов к передаче"
                    : decision.plan.noop
                      ? "Без изменений"
                      : "Нужна проверка"}
                </span>
                {decision.target_absent && (
                  <span className="catalog-warning">
                    Будет создан новый товар
                  </span>
                )}
              </header>
              <DecisionDiff decision={decision} />
              <label className="catalog-check">
                <input
                  type="checkbox"
                  checked={selected.includes(decision.enrollment_id)}
                  disabled={disabled || !canApprove(decision)}
                  onChange={(event) => {
                    setSelected(
                      event.target.checked
                        ? [...selected, decision.enrollment_id]
                        : selected.filter(
                            (id) => id !== decision.enrollment_id,
                          ),
                    );
                    setAcknowledged(false);
                  }}
                />
                Подтвердить изменения товара {productName(decision.source)}
              </label>
            </article>
          ))}
        </div>
        <aside className="products-preview-approval">
          <h3>Перед отправкой</h3>
          <p className="catalog-success">
            <b>{preview.decisions.filter(canApprove).length}</b> готовы к
            передаче
          </p>
          <p className="catalog-warning">
            <b>
              {
                preview.decisions.filter((d) => !canApprove(d) && !d.plan.noop)
                  .length
              }
            </b>{" "}
            требуют проверки
          </p>
          <label className="catalog-check">
            <input
              type="checkbox"
              checked={acknowledged}
              disabled={disabled || !chosen.length}
              onChange={(event) => setAcknowledged(event.target.checked)}
            />
            Я проверил изменения выбранных товаров и разрешаю отправить их в
            указанные сервисы
          </label>
          <button
            disabled={disabled || !acknowledged || !chosen.length}
            onClick={() => {
              if (
                Date.parse(preview.expires_at) > Date.now() &&
                acknowledged &&
                chosen.length
              )
                onApprove(chosen);
            }}
          >
            {busy
              ? "Записываем подтверждение…"
              : `Отправить изменения (${chosen.length})`}
          </button>
          <a className="products-back-link" href="/products">
            ← Вернуться к товарам
          </a>
        </aside>
      </div>
    </section>
  );
}
