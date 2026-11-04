import { useState } from "react";
import type {
  ExemplarInput,
  ExemplarMarkType,
  ExemplarPublic,
  ExemplarSetInput,
} from "./OrdersExemplarTypes";
import { knownOrderField } from "./OrdersTypes";

const markLabels: Record<ExemplarMarkType, string> = {
  mandatory_mark: "Код маркировки",
  jw_uin: "УИН ювелирного изделия",
  imei: "IMEI",
};
const requirementLabels: Record<string, string> = {
  ...markLabels,
  gtd: "Таможенная декларация",
  rnpt: "РНПТ",
  country: "Страна происхождения",
  change_country: "Уточнение страны происхождения",
  weight: "Фактический вес",
};
const modeLabels = {
  unchanged: "Не изменять",
  value: "Указать реальное значение",
  absent: "Подтвердить отсутствие",
};
type Draft = Record<string, Record<string, string>>;
const unitKey = (sku: string, id: string) => JSON.stringify([sku, id]);

export function exemplarInput(
  data: ExemplarPublic,
  draft: Draft,
): ExemplarSetInput | undefined {
  const products = data.products.flatMap((product) => {
    const exemplars = product.ids.flatMap((id) => {
      const values = draft[unitKey(product.sku, id)] ?? {};
      const unit: ExemplarInput = { id };
      for (const field of ["gtd", "rnpt"] as const) {
        if (values[`${field}_mode`] === "value" && values[field]) {
          unit[field] = { presence: "value", value: values[field] };
          unit[`${field}_absent`] = { presence: "zero", value: false };
        }
        if (values[`${field}_mode`] === "absent")
          unit[`${field}_absent`] = { presence: "value", value: true };
      }
      const marks = (Object.keys(markLabels) as ExemplarMarkType[]).flatMap(
        (type) =>
          values[`${type}_clear`] === "yes"
            ? []
            : (values[type] ?? "")
                .split(/\r?\n/)
                .filter((value) => value.length > 0)
                .map((value) => ({ type, value })),
      );
      if (marks.length) unit.marks = { presence: "value", value: marks };
      const clear = (Object.keys(markLabels) as ExemplarMarkType[]).filter(
        (type) => values[`${type}_clear`] === "yes",
      );
      if (clear.length) unit.clear_mark_types = clear;
      return Object.keys(unit).length > 1 ? [unit] : [];
    });
    return exemplars.length ? [{ sku: product.sku, exemplars }] : [];
  });
  const incomplete = Object.values(draft).some((values) =>
    ["gtd", "rnpt"].some(
      (field) => values[`${field}_mode`] === "value" && !values[field],
    ),
  );
  const invalid = products.some((product) =>
    product.exemplars.some(
      (unit) =>
        (unit.marks?.value?.length ?? 0) > 20 ||
        unit.marks?.value?.some((mark) => /[\r\n\0]/.test(mark.value)),
    ),
  );
  return products.length && !incomplete && !invalid ? { products } : undefined;
}

export function OrdersExemplarEditor({
  data,
  disabled,
  onChange,
}: {
  data: ExemplarPublic;
  disabled: boolean;
  onChange: (input: ExemplarSetInput | undefined) => void;
}) {
  const [draft, setDraft] = useState<Draft>({});
  const [page, setPage] = useState(0);
  const units = data.products.flatMap((product) =>
    product.ids.map((id) => ({ product, id })),
  );
  function change(sku: string, id: string, field: string, value: string) {
    const key = unitKey(sku, id);
    const next = { ...draft, [key]: { ...draft[key], [field]: value } };
    setDraft(next);
    onChange(exemplarInput(data, next));
  }
  return (
    <fieldset disabled={disabled}>
      <legend>Данные отдельных экземпляров</legend>
      <p className="orders-note">
        Идентификаторы назначены Ozon. Вводите только реальные сведения с товара
        или документов. Пустой ввод сохраняет прежние данные; очистка
        выполняется отдельно. Коды не показываются в журнале. Несколько кодов
        одного вида вводите по одному на строке, не более 20 на экземпляр.
        Несколько кодов одного вида вводите по одному на строке, не более 20 на
        экземпляр.
      </p>
      {units.slice(page * 10, (page + 1) * 10).map(({ product, id }) => {
        const values = draft[unitKey(product.sku, id)] ?? {};
        return (
          <fieldset key={unitKey(product.sku, id)}>
            <legend>
              SKU {product.sku} · экземпляр {id}
            </legend>
            <p>
              Требования Ozon:{" "}
              {Object.entries(product.required)
                .map(
                  ([kind, field]) =>
                    `${requirementLabels[kind] ?? kind}: ${knownOrderField(field) === true ? "обязательно" : knownOrderField(field) === false ? "не требуется" : "неизвестно"}`,
                )
                .join("; ")}
              .
            </p>
            {["gtd", "rnpt"].map((field) => (
              <div key={field}>
                <label>
                  {requirementLabels[field]}
                  <select
                    value={values[`${field}_mode`] ?? "unchanged"}
                    onChange={(event) =>
                      change(
                        product.sku,
                        id,
                        `${field}_mode`,
                        event.target.value,
                      )
                    }
                  >
                    {Object.entries(modeLabels).map(([mode, title]) => (
                      <option key={mode} value={mode}>
                        {title}
                      </option>
                    ))}
                  </select>
                </label>
                {values[`${field}_mode`] === "value" && (
                  <label>
                    Значение: {requirementLabels[field]}
                    <input
                      autoComplete="off"
                      maxLength={256}
                      value={values[field] ?? ""}
                      onChange={(event) =>
                        change(product.sku, id, field, event.target.value)
                      }
                    />
                  </label>
                )}
              </div>
            ))}
            {(Object.keys(markLabels) as ExemplarMarkType[]).map((type) => (
              <div key={type}>
                <label>
                  {markLabels[type]}
                  <textarea
                    rows={2}
                    autoComplete="off"
                    spellCheck={false}
                    maxLength={8192}
                    value={values[type] ?? ""}
                    disabled={disabled || values[`${type}_clear`] === "yes"}
                    onChange={(event) =>
                      change(product.sku, id, type, event.target.value)
                    }
                  />
                </label>
                {knownOrderField(product.required[type]) === false && (
                  <label className="orders-check">
                    <input
                      type="checkbox"
                      checked={values[`${type}_clear`] === "yes"}
                      onChange={(event) =>
                        change(
                          product.sku,
                          id,
                          `${type}_clear`,
                          event.target.checked ? "yes" : "",
                        )
                      }
                    />
                    Явно удалить прежние данные: {markLabels[type]}
                  </label>
                )}
              </div>
            ))}
          </fieldset>
        );
      })}
      <div className="orders-actions">
        <button
          type="button"
          className="secondary"
          disabled={disabled || page === 0}
          onClick={() => setPage(page - 1)}
        >
          Предыдущие экземпляры
        </button>
        <span>
          Страница {page + 1} из {Math.max(1, Math.ceil(units.length / 10))}
        </span>
        <button
          type="button"
          className="secondary"
          disabled={disabled || (page + 1) * 10 >= units.length}
          onClick={() => setPage(page + 1)}
        >
          Следующие экземпляры
        </button>
      </div>
    </fieldset>
  );
}
