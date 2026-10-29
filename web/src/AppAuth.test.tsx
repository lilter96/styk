import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "./App";
import { setCSRF } from "./api";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  sessionStorage.clear();
  setCSRF("");
  window.history.replaceState(null, "", "/");
});

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 30_000 } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return client;
}

it("preserves the registration form during an explicit signed-out session refresh", async () => {
  let calls = 0;
  let release!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    release = resolve;
  });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    if (String(input) === "/api/session") {
      calls++;
      return calls === 1 ? new Response(null, { status: 401 }) : pending;
    }
    if (String(input) === "/api/auth-mode")
      return new Response(JSON.stringify({ mode: "merchant" }), {
        status: 200,
      });
    throw new Error("unexpected request");
  });
  const client = mount();
  fireEvent.click(
    await screen.findByRole("button", { name: "Создать аккаунт" }),
  );
  fireEvent.change(screen.getByLabelText("Электронная почта"), {
    target: { value: "partial@example.test" },
  });
  fireEvent.change(screen.getByLabelText("Название магазина"), {
    target: { value: "Мой магазин" },
  });
  let refresh!: Promise<void>;
  act(() => {
    refresh = client.invalidateQueries({ queryKey: ["session"] });
  });
  await waitFor(() => expect(calls).toBe(2));
  expect(screen.getByRole("heading", { name: "Создать аккаунт" })).toBeTruthy();
  expect(
    (screen.getByLabelText("Электронная почта") as HTMLInputElement).value,
  ).toBe("partial@example.test");
  await act(async () => {
    release(new Response(null, { status: 401 }));
    await refresh;
  });
  expect(screen.getByRole("heading", { name: "Создать аккаунт" })).toBeTruthy();
  expect(
    (screen.getByLabelText("Название магазина") as HTMLInputElement).value,
  ).toBe("Мой магазин");
});

it("clears workspace data and returns to sign-in when an active session expires", async () => {
  let calls = 0;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const path = String(input);
    if (path === "/api/session") {
      calls++;
      if (calls > 1) return new Response(null, { status: 401 });
      return new Response(
        JSON.stringify({
          user: { id: "user", email: "seller@example.test" },
          workspaces: [{ id: "workspace", name: "Мой магазин", role: "OWNER" }],
          csrf: "csrf",
          expires_at: "2099-01-01T00:00:00Z",
        }),
        { status: 200 },
      );
    }
    if (path === "/api/auth-mode")
      return new Response(JSON.stringify({ mode: "merchant" }), {
        status: 200,
      });
    if (path.startsWith("/api/registrations?"))
      return new Response(JSON.stringify({ accounts: [], truncated: false }), {
        status: 200,
      });
    if (path.startsWith("/api/diagnostics?"))
      return new Response(
        JSON.stringify({ checks: [], batches: {}, mapping_counts: {} }),
        { status: 200 },
      );
    throw new Error("unexpected request");
  });
  const client = mount();
  await screen.findByRole("link", { name: /^Интеграции$/ });
  client.setQueryData(["private-test", "workspace"], "private data");
  await act(async () => {
    await client.invalidateQueries({ queryKey: ["session"] });
  });
  await screen.findByRole("heading", { name: "Войти в STYK" });
  await waitFor(() =>
    expect(client.getQueryData(["private-test", "workspace"])).toBeUndefined(),
  );
  expect(screen.queryByText("Мой магазин")).toBeNull();
});

it("opens an email link while another session is active", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const path = String(input);
    if (path === "/api/session")
      return new Response(
        JSON.stringify({
          user: { id: "user", email: "seller@example.test" },
          workspaces: [{ id: "workspace", name: "Мой магазин", role: "OWNER" }],
          csrf: "csrf",
          expires_at: "2099-01-01T00:00:00Z",
        }),
        { status: 200 },
      );
    if (path === "/api/password-reset")
      return new Response(null, { status: 204 });
    if (path.startsWith("/api/registrations?"))
      return new Response(JSON.stringify({ accounts: [], truncated: false }), {
        status: 200,
      });
    if (path.startsWith("/api/diagnostics?"))
      return new Response(
        JSON.stringify({ checks: [], batches: {}, mapping_counts: {} }),
        { status: 200 },
      );
    throw new Error("unexpected request");
  });
  mount();
  await screen.findByRole("link", { name: /^Интеграции$/ });
  window.location.hash = "reset=" + "a".repeat(43);
  fireEvent(window, new HashChangeEvent("hashchange"));
  await screen.findByRole("heading", { name: "Новый пароль" });
  expect(window.location.hash).toBe("");
  expect(screen.queryByRole("link", { name: /^Интеграции$/ })).toBeNull();
  fireEvent.change(screen.getByLabelText("Новый пароль"), {
    target: { value: "replacement-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить пароль" }));
  await screen.findByText(/Пароль изменён/);
  expect(screen.getByRole("button", { name: "Вернуться в приложение" })).toBeTruthy();
});
