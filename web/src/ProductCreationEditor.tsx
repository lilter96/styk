import { useState } from "react";
import { CatalogSyncCreation } from "./CatalogSyncCreation";
import { CatalogSyncFacts } from "./CatalogSyncFacts";
import { CatalogSyncValue } from "./CatalogSyncValue";
import { BrandLogo } from "./BrandLogo";
import {
  fieldLabel,
  type Enrollment,
  type ProductFacts,
  type Scope,
} from "./CatalogSyncTypes";

export function ProductCreationEditor({
  scope,
  enrollment,
  facts,
  onChange,
  onFacts,
  onClose,
}: {
  scope: Scope;
  enrollment: Enrollment;
  facts?: ProductFacts;
  onChange: (e: Enrollment) => void;
  onFacts: (facts: ProductFacts) => void;
  onClose: () => void;
}) {
  const [step, setStep] = useState(0);
  const values = enrollment.create!.values;
  const name = values.name;
  const nameReady =
    name?.state === "known" &&
    typeof name.data === "string" &&
    !!name.data.trim();
  const commercialReady =
    !!enrollment.target.offer_id?.trim() &&
    ["price", "currency", "dimensions"].every(
      (f) => values[f]?.state === "known",
    ) &&
    enrollment.create?.extensions?.vat?.state === "known";
  const labels = ["Основное", "Цена и размеры", "Категория", "Проверка"];
  const ozon = enrollment.target.account.provider === "ozon";
  if (!ozon)
    return (
      <>
        <CatalogSyncCreation enrollment={enrollment} onChange={onChange} />
        <p>Изменения хранятся только до ухода со страницы.</p>
        <button onClick={onClose}>Вернуться к проверке</button>
      </>
    );
  return (
    <div className="products-creation-wizard">
      <div className="products-creation-pair">
        <BrandLogo provider="moysklad" decorative />
        МойСклад <span>→</span>
        <BrandLogo provider="ozon" decorative />
        Ozon <span className="mapping-chip warning">Не создан</span>
      </div>
      <ol className="products-creation-steps">
        {labels.map((label, index) => (
          <li
            key={label}
            className={index < step ? "done" : ""}
            aria-current={index === step ? "step" : undefined}
          >
            <span>{index < step ? "✓" : index + 1}</span>
            {label}
          </li>
        ))}
      </ol>
      <div className="products-creation-layout">
        <section>
          <h3>{labels[step]}</h3>
          {step < 2 && (
            <CatalogSyncCreation
              step={step === 0 ? "basic" : "commercial"}
              enrollment={enrollment}
              onChange={onChange}
            />
          )}
          {step === 2 && (
            <CatalogSyncFacts
              scope={scope}
              facts={facts}
              onChange={onFacts}
              initiallyOpen
            />
          )}
          {step === 3 && (
            <>
              <p className="products-notice">
                Изменения хранятся только до ухода со страницы. Данные ещё не
                отправлены. Вернитесь к списку и выполните серверную проверку
                карточки.
              </p>
              <dl className="products-creation-summary">
                {Object.entries({
                  ...values,
                  ...enrollment.create?.extensions,
                }).map(([field, value]) => (
                  <div key={field}>
                    <dt>{fieldLabel(field)}</dt>
                    <dd>
                      <CatalogSyncValue field={field} value={value} />
                    </dd>
                  </div>
                ))}
              </dl>
              {facts?.category_id && values.category?.state !== "known" && (
                <dl className="products-creation-summary">
                  <div>
                    <dt>Категория Ozon</dt>
                    <dd>{facts.category_id}</dd>
                  </div>
                  {Object.entries(facts.attributes).map(([id, value]) => (
                    <div key={id}>
                      <dt>Характеристика {id}</dt>
                      <dd>{facts.choices?.[id]?.name || value}</dd>
                    </div>
                  ))}
                </dl>
              )}
              {!facts?.category_id && (
                <p className="catalog-warning">
                  Категория ещё не выбрана. Серверная проверка покажет
                  обязательные поля.
                </p>
              )}
            </>
          )}
        </section>
        <aside>
          <h3>Что понадобится</h3>
          <ul>
            <li>Название и артикул</li>
            <li>Цена, НДС и габариты</li>
            <li>Категория и обязательные характеристики</li>
            <li>Подтверждение создания</li>
          </ul>
          <p>
            Готовность проверим по требованиям Ozon. Сохранение данных ничего не
            отправляет.
          </p>
        </aside>
      </div>
      <footer className="products-creation-footer">
        <span>
          Изменения хранятся только до ухода со страницы. Данные ещё не
          отправлены
        </span>
        <button
          className="secondary"
          onClick={() => (step ? setStep(step - 1) : onClose())}
        >
          Назад
        </button>
        <button className="secondary" onClick={onClose}>
          Вернуться к списку
        </button>
        {step < 3 ? (
          <button
            disabled={
              step === 0 ? !nameReady : step === 1 ? !commercialReady : false
            }
            onClick={() => setStep(step + 1)}
          >
            Далее: {labels[step + 1].toLowerCase()} →
          </button>
        ) : (
          <button onClick={onClose}>Вернуться к проверке</button>
        )}
      </footer>
    </div>
  );
}
