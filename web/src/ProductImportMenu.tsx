import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import { BrandLogo } from "./BrandLogo";
import { Icon } from "./UI";
export function ProductImportMenu() {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return (
    <div
      className="products-import-menu"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          setOpen(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        ref={trigger}
        className="primary-button"
        aria-expanded={open}
        aria-controls="products-import-directions"
        onClick={() => setOpen(!open)}
      >
        Импорт товаров <Icon name="chevron-down" size="button" />
      </button>
      {open && (
        <nav id="products-import-directions" aria-label="Направление импорта">
          <Link
            to="/products/import/ozon-to-moysklad"
            onClick={() => setOpen(false)}
          >
            <BrandLogo provider="ozon" decorative />
            Ozon → МойСклад
          </Link>
          <Link
            to="/products/import/moysklad-to-ozon"
            onClick={() => setOpen(false)}
          >
            <BrandLogo provider="moysklad" decorative />
            МойСклад → Ozon
          </Link>
          <Link to="/products/import" onClick={() => setOpen(false)}>
            Об импорте товаров
          </Link>
        </nav>
      )}
    </div>
  );
}
