import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  ordersURL,
  sameOrderScope,
  orderRequestError,
  type NativeScope,
  type OrderPosting,
  type OrderScope,
} from "./OrdersTypes";

export type OrderCountryDraft = {
  sku: string;
  iso: string;
  confirmed: boolean;
};
type Countries = {
  scope: NativeScope;
  posting_number: string;
  source_revision: string;
  required_skus: string[];
  countries: { name: string; country_iso_code: string }[];
};
export function OrdersCountry({
  scope,
  posting,
  disabled,
  onChange,
}: {
  scope: OrderScope;
  posting: OrderPosting;
  disabled: boolean;
  onChange: (value: OrderCountryDraft | undefined) => void;
}) {
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [sku, setSKU] = useState("");
  const [iso, setISO] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const mounted = useRef(false);
  const previousAvailable = useRef(false);
  const countries = useQuery({
    queryKey: [
      "orders-countries",
      scope,
      posting.posting_number,
      posting.evidence.revision,
      query,
    ],
    queryFn: async ({ signal }) => {
      const value = await api<Countries>(
        ordersURL("countries", scope, {
          posting_number: posting.posting_number,
          search: query,
        }),
        signal,
      );
      if (
        !sameOrderScope(value.scope, scope) ||
        value.posting_number !== posting.posting_number ||
        value.source_revision !== posting.evidence.revision ||
        !Array.isArray(value.countries) ||
        value.countries.length > 1000 ||
        !Array.isArray(value.required_skus) ||
        value.required_skus.some(
          (id) => !posting.lines.some((line) => line.identity.sku === id),
        ) ||
        value.countries.some(
          (country) =>
            !/^[A-Z]{2}$/.test(country.country_iso_code) || !country.name,
        )
      )
        throw new Error("country evidence mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
  const available =
    !countries.isFetching &&
    !countries.isError &&
    countries.data?.required_skus.includes(sku) &&
    countries.data.countries.some(
      (country) => country.country_iso_code === iso,
    );
  useEffect(() => {
    if (!mounted.current || (previousAvailable.current && !available))
      onChange(undefined);
    mounted.current = true;
    previousAvailable.current = !!available;
  }, [available, onChange]);
  function change(nextSKU: string, nextISO: string, nextConfirmed: boolean) {
    setSKU(nextSKU);
    setISO(nextISO);
    setConfirmed(nextConfirmed);
    onChange(
      nextConfirmed &&
        countries.data?.required_skus.includes(nextSKU) &&
        countries.data.countries.some(
          (country) => country.country_iso_code === nextISO,
        )
        ? { sku: nextSKU, iso: nextISO, confirmed: true }
        : undefined,
    );
  }
  return (
    <fieldset disabled={disabled}>
      <legend>Фактическая страна происхождения товара</legend>
      <p className="orders-note">
        Выберите реальную страну по товару или документам. Это не страна
        доставки и не страна продавца. STYK не подставляет её автоматически.
      </p>
      {countries.isFetching && (
        <p role="status">Читаем справочник стран Ozon…</p>
      )}
      {countries.isError && (
        <p role="alert">{orderRequestError(countries.error)}</p>
      )}
      <label>
        Поиск страны
        <input
          maxLength={100}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
      </label>
      <button
        type="button"
        className="secondary"
        disabled={countries.isFetching}
        onClick={() => {
          change(sku, "", false);
          setQuery(search.trim());
          if (query === search.trim()) void countries.refetch();
        }}
      >
        Найти страну
      </button>
      <label>
        Товар, для которого Ozon требует страну
        <select
          value={sku}
          disabled={countries.isFetching || countries.isError}
          onChange={(event) => change(event.target.value, iso, false)}
        >
          <option value="">Выберите SKU</option>
          {(countries.data?.required_skus ?? []).map((id) => (
            <option key={id} value={id}>
              SKU {id} ·{" "}
              {posting.lines.find((line) => line.identity.sku === id)?.identity
                .offer_id ?? "Товар отправления"}
            </option>
          ))}
        </select>
      </label>
      <label>
        Страна из справочника Ozon
        <select
          value={iso}
          disabled={countries.isFetching || countries.isError}
          onChange={(event) => change(sku, event.target.value, false)}
        >
          <option value="">Выберите фактическую страну</option>
          {(countries.data?.countries ?? []).map((country) => (
            <option
              key={country.country_iso_code}
              value={country.country_iso_code}
            >
              {country.name} ({country.country_iso_code})
            </option>
          ))}
        </select>
      </label>
      {!countries.isFetching &&
        !countries.isError &&
        !countries.data?.required_skus.length && (
          <p className="orders-warning">
            Для этого отправления нет подтверждённого требования указать страну.
            Запрос без основания не отправляется.
          </p>
        )}
      <label className="orders-check">
        <input
          type="checkbox"
          checked={confirmed}
          disabled={!available}
          onChange={(event) => change(sku, iso, event.target.checked)}
        />
        Подтверждаю фактическую страну происхождения выбранного товара по
        имеющимся сведениям.
      </label>
      <p className="orders-note">
        Ozon не возвращает установленную страну отдельным чтением. STYK проверит
        подтверждение запроса и снятие требования; это не независимое
        подтверждение страны.
      </p>
    </fieldset>
  );
}
