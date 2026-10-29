import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MerchantAuth } from "./MerchantAuth";
import { setCSRF } from "./api";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  setCSRF("");
  window.history.replaceState(null, "", "/");
});

it("registers a workspace and keeps the password out of browser storage", async () => {
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(new Response(null, { status: 202 }));
  render(<MerchantAuth onDone={() => {}} />);
  fireEvent.click(screen.getByRole("button", { name: "Создать аккаунт" }));
  fireEvent.change(screen.getByLabelText("Электронная почта"), {
    target: { value: "seller@example.test" },
  });
  fireEvent.change(screen.getByLabelText("Пароль"), {
    target: { value: "synthetic-password" },
  });
  fireEvent.change(screen.getByLabelText("Название магазина"), {
    target: { value: "Магазин" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Создать аккаунт" }));
  await screen.findByText(/Если адрес подходит/);
  const request = fetcher.mock.calls[0];
  expect(request[0]).toBe("/api/registration");
  expect(JSON.parse(String(request[1]?.body))).toMatchObject({
    email: "seller@example.test",
    workspace: "Магазин",
  });
  expect(JSON.stringify(localStorage)).not.toContain("synthetic-password");
  expect(JSON.stringify(sessionStorage)).not.toContain("synthetic-password");
  expect((screen.getByLabelText("Пароль") as HTMLInputElement).value).toBe("");
});

it("removes a verification token from the address and submits it once", async () => {
  const token = "a".repeat(43);
  window.history.replaceState(null, "", "/#verify=" + token);
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(new Response(null, { status: 204 }));
  render(<MerchantAuth onDone={() => {}} />);
  expect(window.location.hash).toBe("");
  fireEvent.change(screen.getByLabelText("Новый пароль"), {
    target: { value: "verification-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Подтвердить адрес" }));
  await screen.findByText(/Почта подтверждена/);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({
    token,
    password: "verification-password",
  });
});

it("reports rejected credentials without recording them", async () => {
  const done = vi.fn();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(null, { status: 401 }),
  );
  render(<MerchantAuth onDone={done} />);
  fireEvent.change(screen.getByLabelText("Электронная почта"), {
    target: { value: "seller@example.test" },
  });
  fireEvent.change(screen.getByLabelText("Пароль"), {
    target: { value: "wrong-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Войти" }));
  await waitFor(() =>
    expect(screen.getByRole("alert").textContent).toContain("Не удалось войти"),
  );
  expect(done).not.toHaveBeenCalled();
  expect(JSON.stringify(sessionStorage)).not.toContain("wrong-password");
});
