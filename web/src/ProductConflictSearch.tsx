import { useEffect, useRef, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, params, type Page } from "./api";
import type { Scope } from "./Confirmation";
import type { MappingTarget } from "./ProductReview";

export function ProductConflictSearch({
  scope,
  offer,
  choose,
}: {
  scope: Scope;
  offer: string;
  choose: (target: MappingTarget) => void;
}) {
  const [search, setSearch] = useState(offer);
  const [query, setQuery] = useState(offer);
  const [page, setPage] = useState(1);
  const pendingPage = useRef<number | null>(null);
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (search.trim() === query) return;
    const timer = setTimeout(() => {
      setQuery(search.trim());
      pendingPage.current = null;
      setPage(1);
    }, 300);
    return () => clearTimeout(timer);
  }, [search, query]);
  const result = useQuery({
    queryKey: ["conflict-targets", scope, query, page],
    queryFn: ({ signal }) =>
      api<Page<MappingTarget> & { total?: number }>(
        "/api/moysklad-products?" +
          params({
            tenant: scope.tenant,
            moysklad_account: scope.moy,
            ozon_account: scope.ozon,
            query,
            searchBy: "all",
            page,
            pageSize: 10,
          }),
        signal,
      ),
    retry: false,
    placeholderData: keepPreviousData,
  });
  useEffect(() => {
    if (pendingPage.current !== page || result.isFetching) return;
    pendingPage.current = null;
    list.current?.querySelector<HTMLButtonElement>("button")?.focus();
  }, [page, result.isFetching]);
  function changePage(next: number) {
    pendingPage.current = next;
    setPage(next);
  }
  return (
    <section className="conflict-search" aria-label="Поиск товара МойСклад">
      <label>
        Найти товар в МойСклад
        <input
          autoFocus
          type="search"
          aria-label="Название, артикул или код товара"
          placeholder="Название, артикул или код"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </label>
      <p className="conflict-muted">
        Выбор откроет сравнение. Связь останется прежней до подтверждения.
      </p>
      {result.isFetching && <p role="status">Ищем товары…</p>}
      {result.isError && (
        <div role="alert">
          Не удалось загрузить товары.{" "}
          <button className="text-action" onClick={() => void result.refetch()}>
            Повторить поиск
          </button>
        </div>
      )}
      {!result.isFetching && !result.isError && !result.data?.items.length && (
        <div className="conflict-empty">
          <h3>Товары не найдены</h3>
          <p>Попробуйте другое название или артикул.</p>
          {search && (
            <button className="secondary" onClick={() => setSearch("")}>
              Показать все товары
            </button>
          )}
        </div>
      )}
      <div
        className="conflict-options"
        ref={list}
        aria-busy={result.isFetching}
      >
        {result.data?.items.map((t) => (
          <button
            className="conflict-option"
            key={t.kind + ":" + t.id}
            disabled={result.isFetching || search.trim() !== query}
            onClick={() => choose(t)}
          >
            <span>
              <b title={t.name}>{t.name || "Товар без названия"}</b>
              <small
                title={`Артикул: ${t.article || "не указан"}${t.code ? ` · Код: ${t.code}` : ""}`}
              >
                Артикул: {t.article || "не указан"}
                {t.code ? ` · Код: ${t.code}` : ""}
              </small>
              {t.kind === "variant" && <small>Модификация товара</small>}
            </span>
            <span>Сравнить</span>
          </button>
        ))}
      </div>
      {(page > 1 || result.data?.has_next) && (
        <div className="conflict-search-pager">
          <button
            className="secondary"
            disabled={page === 1 || result.isFetching}
            onClick={() => changePage(page - 1)}
          >
            Назад
          </button>
          <span>Страница {page}</span>
          <button
            className="secondary"
            disabled={!result.data?.has_next || result.isFetching}
            onClick={() => changePage(page + 1)}
          >
            Далее
          </button>
        </div>
      )}
    </section>
  );
}
