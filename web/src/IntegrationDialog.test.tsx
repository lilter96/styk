import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { IntegrationDialog } from "./IntegrationDialog";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  document.body.style.overflow = "";
  document.body.style.paddingRight = "";
});

it("focuses a usable body control and traps Tab without entering closed technical details", () => {
  render(
    <IntegrationDialog title="Настройки Ozon" onClose={vi.fn()}>
      <button disabled>Недоступно</button>
      <div hidden>
        <button>Скрыто</button>
      </div>
      <label>
        Аккаунт
        <select>
          <option>Первый аккаунт</option>
        </select>
      </label>
      <button>Проверить подключение</button>
      <details>
        <summary>Технические детали</summary>
        <button>Копировать идентификатор</button>
      </details>
    </IntegrationDialog>,
  );
  const dialog = screen.getByRole("dialog", { name: "Настройки Ozon" });
  expect(dialog.getAttribute("aria-modal")).toBe("true");
  expect(document.activeElement).toBe(
    screen.getByRole("combobox", { name: "Аккаунт" }),
  );
  const close = screen.getByRole("button", { name: "Закрыть" });
  const summary = screen.getByText("Технические детали");
  close.focus();
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(summary);
  fireEvent.keyDown(summary, { key: "Tab" });
  expect(document.activeElement).toBe(close);

  fireEvent.click(summary);
  const copy = screen.getByRole("button", { name: "Копировать идентификатор" });
  copy.focus();
  fireEvent.keyDown(copy, { key: "Tab" });
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(copy);
});

it("closes through Escape, the backdrop or the labelled close control, while inner clicks stay open", () => {
  const close = vi.fn();
  render(
    <IntegrationDialog title="Настройки МойСклад" onClose={close}>
      <button>Проверить подключение</button>
    </IntegrationDialog>,
  );
  const dialog = screen.getByRole("dialog", { name: "Настройки МойСклад" });
  fireEvent.click(dialog);
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить подключение" }),
  );
  expect(close).not.toHaveBeenCalled();
  fireEvent.keyDown(dialog, { key: "Escape" });
  expect(close).toHaveBeenCalledTimes(1);
  fireEvent.click(dialog.parentElement!);
  expect(close).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("button", { name: "Закрыть" }));
  expect(close).toHaveBeenCalledTimes(3);
});

it("restores trigger focus and the original body styles after closing, preserving scrollbar width", () => {
  document.body.style.overflow = "scroll";
  document.body.style.paddingRight = "7px";
  vi.spyOn(document.documentElement, "clientWidth", "get").mockReturnValue(
    window.innerWidth - 20,
  );
  function Harness() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button onClick={() => setOpen(true)}>Настройки</button>
        {open && (
          <IntegrationDialog
            title="Настройки Ozon"
            onClose={() => setOpen(false)}
          >
            <button>Проверить подключение</button>
          </IntegrationDialog>
        )}
      </>
    );
  }
  render(<Harness />);
  const trigger = screen.getByRole("button", {
    name: "Настройки",
  });
  trigger.focus();
  fireEvent.click(trigger);
  expect(document.body.style.overflow).toBe("hidden");
  expect(document.body.style.paddingRight).toBe("27px");
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Проверить подключение" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Закрыть" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  expect(document.body.style.overflow).toBe("scroll");
  expect(document.body.style.paddingRight).toBe("7px");
});

it("keeps focus during rerenders and uses the latest close callback", () => {
  const firstClose = vi.fn(),
    nextClose = vi.fn();
  const body = (
    <>
      <label>
        Аккаунт
        <select>
          <option>Первый аккаунт</option>
        </select>
      </label>
      <button>Проверить подключение</button>
    </>
  );
  const view = render(
    <IntegrationDialog title="Настройки Ozon" onClose={firstClose}>
      {body}
    </IntegrationDialog>,
  );
  const probe = screen.getByRole("button", { name: "Проверить подключение" });
  probe.focus();
  view.rerender(
    <IntegrationDialog title="Настройки Ozon" onClose={nextClose}>
      {body}
    </IntegrationDialog>,
  );
  expect(document.activeElement).toBe(probe);
  fireEvent.keyDown(probe, { key: "Escape" });
  expect(firstClose).not.toHaveBeenCalled();
  expect(nextClose).toHaveBeenCalledTimes(1);
});
