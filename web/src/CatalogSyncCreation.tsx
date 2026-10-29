import {
  displayValue,
  providerLabel,
  type CatalogProduct,
  type CatalogValue,
  type Enrollment,
  type Scope,
} from "./CatalogSyncTypes";

export function newCatalogCreation(
  product: CatalogProduct,
  scope: Scope,
): Enrollment {
  const id = crypto.randomUUID();
  const provider =
    product.identity.account.provider === "ozon" ? "moysklad" : "ozon";
  const values: Record<string, CatalogValue> = {};
  for (const field of ["name", "description"]) {
    const value = product.fields?.[field];
    if (
      value?.state === "known" &&
      typeof value.data === "string" &&
      value.data.trim()
    )
      values[field] = value;
  }
  return {
    id: `create-${id}`,
    source: product.identity,
    target: {
      id: "",
      entity_type: "product",
      account: {
        provider,
        id: provider === "moysklad" ? scope.moy : scope.ozon,
      },
    },
    create: { id, external_code: `STYK-CATALOG-${id}`, values },
    automatic: false,
    automatic_fields: [],
  };
}

function observedText(value?: CatalogValue) {
  return value?.state === "known" && typeof value.data === "string"
    ? value.data
    : "";
}

export function CatalogSyncCreation({
  enrollment,
  onChange,
}: {
  enrollment: Enrollment;
  onChange: (enrollment: Enrollment) => void;
}) {
  const creation = enrollment.create!;
  const ozon = enrollment.target.account.provider === "ozon";
  function setValue(field: string, text: string) {
    const values = { ...creation.values };
    if (!text) delete values[field];
    else values[field] = { state: "known", data: text };
    onChange({ ...enrollment, create: { ...creation, values } });
  }
  return (
    <div className="catalog-create-form">
      <p className="catalog-warning">
        Будет создан новый товар в{" "}
        {providerLabel(enrollment.target.account.provider)}. STYK сначала
        проверит отсутствие такого товара. Название и описание взяты из
        выбранного товара — проверьте их.
      </p>
      <label>
        Название нового товара
        <input
          value={observedText(creation.values.name)}
          onChange={(event) => setValue("name", event.target.value)}
        />
      </label>
      {!observedText(creation.values.name).trim() && (
        <p className="catalog-warning">Укажите название товара.</p>
      )}
      <label>
        Описание нового товара
        <textarea
          rows={3}
          value={observedText(creation.values.description)}
          onChange={(event) => setValue("description", event.target.value)}
        />
      </label>
      {ozon && (
        <>
          <label>
            Артикул нового товара в Ozon
            <input
              value={enrollment.target.offer_id || ""}
              onChange={(event) =>
                onChange({
                  ...enrollment,
                  target: {
                    ...enrollment.target,
                    offer_id: event.target.value,
                  },
                })
              }
            />
          </label>
          {!enrollment.target.offer_id && (
            <p className="catalog-warning">
              Для создания в Ozon укажите отдельный артикул продавца.
            </p>
          )}
          <p>
            Укажите фактические данные. Цена, валюта, НДС, размеры, вес и
            характеристики не подставляются автоматически.
          </p>
          <div className="catalog-policies">
            <label>
              Цена нового товара
              <input
                inputMode="decimal"
                value={observedText(creation.values.price)}
                onChange={(event) => setValue("price", event.target.value)}
              />
            </label>
            <label>
              Валюта нового товара
              <input
                maxLength={3}
                value={observedText(creation.values.currency)}
                onChange={(event) => setValue("currency", event.target.value)}
              />
              <small>Трёхбуквенный код валюты.</small>
            </label>
            <label>
              НДС для Ozon
              <input
                inputMode="decimal"
                aria-label="НДС для Ozon"
                value={observedText(creation.extensions?.vat)}
                onChange={(event) => {
                  const extensions = { ...creation.extensions };
                  if (event.target.value)
                    extensions.vat = {
                      state: "known",
                      data: event.target.value,
                    };
                  else delete extensions.vat;
                  onChange({
                    ...enrollment,
                    create: { ...creation, extensions },
                  });
                }}
              />
              <small>
                Фактическое значение, которое принимает Ozon для вашего товара.
              </small>
            </label>
          </div>
          <CreationDimensions enrollment={enrollment} onChange={onChange} />
          <label>
            Ссылки на изображения нового товара (по одной на строку)
            <textarea
              rows={3}
              value={
                creation.values.images?.state === "known" &&
                Array.isArray(creation.values.images.data)
                  ? creation.values.images.data.join("\n")
                  : ""
              }
              onChange={(event) => {
                const images = event.target.value
                  .split("\n")
                  .map((line) => line.trim())
                  .filter(Boolean);
                const values = { ...creation.values };
                if (images.length)
                  values.images = { state: "known", data: images };
                else delete values.images;
                onChange({ ...enrollment, create: { ...creation, values } });
              }}
            />
            <small>
              Укажите публичные адреса HTTPS. Поддержку проверит сервис перед
              отправкой.
            </small>
          </label>
        </>
      )}
      <details>
        <summary>Идентификатор создания</summary>
        <p>
          Этот идентификатор сохранится с запросом и позволит сверить результат
          без повторного создания.
        </p>
        <p>{creation.external_code}</p>
      </details>
    </div>
  );
}

function CreationDimensions({
  enrollment,
  onChange,
}: {
  enrollment: Enrollment;
  onChange: (enrollment: Enrollment) => void;
}) {
  const create = enrollment.create!;
  const current = create.values.dimensions;
  const dimensions: Record<string, unknown> =
    current?.state === "known" &&
    current.data &&
    typeof current.data === "object"
      ? (current.data as Record<string, unknown>)
      : {};
  function change(field: string, value: string, number: boolean) {
    const data = { ...dimensions };
    if (!value || (number && !Number.isFinite(Number(value))))
      delete data[field];
    else data[field] = number ? Number(value) : value;
    const values = { ...create.values };
    if (Object.keys(data).length) values.dimensions = { state: "known", data };
    else delete values.dimensions;
    onChange({ ...enrollment, create: { ...create, values } });
  }
  return (
    <fieldset>
      <legend>Размеры и вес нового товара</legend>
      <div className="catalog-policies">
        {[
          ["height", "Высота"],
          ["width", "Ширина"],
          ["depth", "Глубина"],
          ["weight", "Вес"],
        ].map(([field, label]) => (
          <label key={field}>
            {label}
            <input
              type="number"
              step="1"
              value={
                typeof dimensions[field] === "number"
                  ? String(dimensions[field])
                  : ""
              }
              onChange={(event) => change(field, event.target.value, true)}
            />
          </label>
        ))}
        {[
          [
            "dimension_unit",
            "Единица размера",
            [
              ["mm", "Миллиметры"],
              ["cm", "Сантиметры"],
              ["in", "Дюймы"],
            ],
          ],
          [
            "weight_unit",
            "Единица веса",
            [
              ["g", "Граммы"],
              ["kg", "Килограммы"],
              ["lb", "Фунты"],
            ],
          ],
        ].map(([field, label, options]) => (
          <label key={String(field)}>
            {String(label)}
            <select
              value={
                typeof dimensions[String(field)] === "string"
                  ? String(dimensions[String(field)])
                  : ""
              }
              onChange={(event) =>
                change(String(field), event.target.value, false)
              }
            >
              <option value="">Выберите единицу</option>
              {(options as string[][]).map(([value, text]) => (
                <option key={value} value={value}>
                  {text}
                </option>
              ))}
            </select>
          </label>
        ))}
      </div>
      <small>
        Значения и единицы измерения проверит сервис. Неизвестный размер нельзя
        заменить нулём.
      </small>
      <span className="catalog-sr-only">
        Текущее значение: {displayValue(current)}
      </span>
    </fieldset>
  );
}
