import { useRef } from "react";

export function OrdersTabs<T extends string>({
  id,
  label,
  tabs,
  value,
  onChange,
}: {
  id: string;
  label: string;
  tabs: readonly { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  const list = useRef<HTMLDivElement>(null);
  return (
    <div ref={list} className="orders-tabs" role="tablist" aria-label={label}>
      {tabs.map((tab, index) => (
        <button
          type="button"
          role="tab"
          className="orders-tab"
          key={tab.value}
          id={`${id}-${tab.value}-tab`}
          aria-controls={`${id}-${tab.value}-panel`}
          aria-selected={value === tab.value}
          tabIndex={value === tab.value ? 0 : -1}
          onClick={() => onChange(tab.value)}
          onKeyDown={(event) => {
            const next =
              event.key === "ArrowRight"
                ? (index + 1) % tabs.length
                : event.key === "ArrowLeft"
                  ? (index + tabs.length - 1) % tabs.length
                  : event.key === "Home"
                    ? 0
                    : event.key === "End"
                      ? tabs.length - 1
                      : -1;
            if (next < 0) return;
            event.preventDefault();
            onChange(tabs[next].value);
            const controls =
              list.current?.querySelectorAll<HTMLButtonElement>("button");
            controls?.item(next)?.focus();
          }}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
