import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkspaceSettings } from "./WorkspaceSettings";
import { setCSRF } from "./api";

afterEach(() => {
  cleanup();
  setCSRF("");
  vi.restoreAllMocks();
});

it("changes a member role with the current workspace and CSRF token", async () => {
  setCSRF("synthetic-csrf");
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      if (init?.method === "POST") return new Response(null, { status: 204 });
      if (String(input).endsWith("/members"))
        return Response.json({ members: [] });
      return Response.json({ events: [] });
    });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <WorkspaceSettings
        workspace="workspace-a"
        role="OWNER"
        switchTo={() => {}}
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Настройки пространства"));
  fireEvent.change(screen.getByLabelText("Почта участника"), {
    target: { value: "other@example.test" },
  });
  fireEvent.change(screen.getByLabelText("Роль"), {
    target: { value: "VIEWER" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить доступ" }));
  await waitFor(() =>
    expect(fetcher.mock.calls.some((call) => call[1]?.method === "POST")).toBe(
      true,
    ),
  );
  const mutation = fetcher.mock.calls.find(
    (call) => call[1]?.method === "POST",
  );
  expect(mutation?.[0]).toBe("/api/workspaces/workspace-a/members");
  expect(JSON.parse(String(mutation?.[1]?.body))).toEqual({
    email: "other@example.test",
    role: "VIEWER",
  });
  expect(new Headers(mutation?.[1]?.headers).get("X-STYK-CSRF")).toBe(
    "synthetic-csrf",
  );
  client.clear();
});
