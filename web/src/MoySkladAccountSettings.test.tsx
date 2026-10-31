import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { MoySkladAccountSettingsPage } from "./MoySkladAccountSettingsPage";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";
import {
  CredentialNotice,
  useCredentialSubmission,
} from "./CredentialSubmission";
import { useImportSubmission } from "./ImportSubmission";
import { api, type Account } from "./api";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
const scope = {
  tenant: "merchant-a",
  account: "moy-a",
  provider: "moysklad" as const,
};
const account = {
  source: "moysklad",
  account: "moy-a",
  registered_at: "2026-10-30T00:00:00Z",
};
function fixture(
  options: {
    saveFailure?: boolean;
    probeFailure?: boolean;
    metadataFailure?: boolean;
  } = {},
) {
  let settings = {
    name: "Основной аккаунт",
    description: "Склад",
    revision: 0,
  };
  let meta = {
    ...scope,
    name: "Основной аккаунт",
    revision: 2,
    enabled: true,
    checked_at: "2026-10-30T00:00:00Z",
    updated_at: "2026-10-30T00:00:00Z",
    last_request_id: "old",
  };
  const mutations: { url: string; body: Record<string, unknown> }[] = [];
  const fetch = vi.fn(
    async (path: string | URL | Request, init?: RequestInit) => {
      const url = String(path);
      const body = init?.body ? JSON.parse(String(init.body)) : {};
      const json = (value: unknown, status = 200) =>
        new Response(JSON.stringify(value), {
          status,
          headers: { "Content-Type": "application/json" },
        });
      if (init?.method === "POST") {
        mutations.push({ url, body });
        if (url.endsWith("/settings")) {
          if (options.saveFailure)
            return json({ error: "SETTINGS_VERSION_CONFLICT" }, 409);
          settings = {
            name: body.name,
            description: body.description,
            revision: settings.revision + 1,
          };
          return json(settings);
        }
        if (url.endsWith("/probe"))
          return json(
            options.probeFailure
              ? { status: "VERIFICATION_FAILED", reason: "INVALID_CREDENTIALS" }
              : { status: "VERIFIED" },
          );
        meta = {
          ...meta,
          revision: meta.revision + 1,
          enabled: !url.endsWith("/disconnect"),
          last_request_id: body.request_id,
        };
        return json({ credential: meta });
      }
      if (url.startsWith("/api/registrations"))
        return json({ accounts: [{ ...account, name: settings.name }] });
      if (url.startsWith("/api/integrations/moysklad/settings"))
        return options.metadataFailure
          ? json({ error: "UNAVAILABLE" }, 503)
          : json(settings);
      if (url.startsWith("/api/integrations/moysklad/credentials"))
        return json({ storage_available: true, credential: meta });
      if (url.startsWith("/api/integrations?"))
        return json({
          providers: [
            {
              provider: "moysklad",
              account: scope.account,
              status: meta.enabled ? "VERIFIED" : "NOT_CONFIGURED",
            },
          ],
        });
      if (url.startsWith("/api/imports"))
        return json({ items: [], has_next: false, offset: 0 });
      throw new Error("Unexpected request " + url);
    },
  );
  vi.stubGlobal("fetch", fetch);
  function Harness() {
    const credentials = useCredentialSubmission();
    const imports = useImportSubmission();
    const accounts = useQuery({
      queryKey: ["accounts", scope.tenant],
      queryFn: () => api<{ accounts: Account[] }>("/api/registrations"),
    });
    return (
      <>
        <WorkspaceSwitcher
          marketplace="ozon"
          onMarketplaceChange={() => {}}
          accounts={accounts.data?.accounts || []}
          account=""
          onAccountChange={() => {}}
          onAdd={() => {}}
          moyLabel={accounts.data?.accounts[0]?.name || ""}
          moyAccount={scope.account}
          loading={false}
          error={false}
          addDisabled={false}
        />
        <CredentialNotice
          submission={credentials}
          tenant={scope.tenant}
          restore={() => {}}
        />
        <MoySkladAccountSettingsPage
          tenant={scope.tenant}
          account={scope.account}
          accounts={[account]}
          accountsLoading={false}
          credentials={credentials}
          imports={imports}
        />
      </>
    );
  }
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter
        initialEntries={["/integrations/moysklad/settings?account=moy-a"]}
      >
        <Harness />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { fetch, mutations };
}
it("keeps edits across sections and updates the sidebar only after saving metadata", async () => {
  const { mutations } = fixture();
  const name = await screen.findByLabelText(/Название аккаунта/);
  expect(
    (
      screen.getByRole("button", {
        name: "Сохранить изменения",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.change(name, { target: { value: "Розничный склад" } });
  fireEvent.change(screen.getByLabelText(/Описание/), {
    target: { value: "Для розничных заказов" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Подключение" }));
  fireEvent.click(screen.getByRole("button", { name: "Основная информация" }));
  expect(
    (screen.getByLabelText(/Название аккаунта/) as HTMLInputElement).value,
  ).toBe("Розничный склад");
  fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
  await screen.findByText("Изменения сохранены");
  await waitFor(() =>
    expect(
      document.querySelector(".fixed-moy-account strong")?.textContent,
    ).toBe("Розничный склад"),
  );
  expect(mutations).toHaveLength(1);
  expect(mutations[0].url).toContain("/settings");
  expect(mutations[0].body).not.toHaveProperty("secret");
});
it("validates empty names and preserves edits after a version conflict", async () => {
  fixture({ saveFailure: true });
  const name = await screen.findByLabelText(/Название аккаунта/);
  fireEvent.change(name, { target: { value: " " } });
  expect(
    (
      screen.getByRole("button", {
        name: "Сохранить изменения",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.change(name, { target: { value: "Мой склад" } });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
  await screen.findByText(/Настройки изменились в другой вкладке/);
  expect((name as HTMLInputElement).value).toBe("Мой склад");
});
it("replaces the token with the existing revision protocol and clears plaintext", async () => {
  const { mutations } = fixture();
  await screen.findByLabelText(/Название аккаунта/);
  fireEvent.click(screen.getByRole("button", { name: "Подключение" }));
  fireEvent.click(screen.getByRole("button", { name: "Заменить токен" }));
  const input = screen.getByLabelText("Новый API-токен") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "synthetic-replacement" } });
  fireEvent.click(
    screen.getByRole("checkbox", { name: /аккаунт принадлежит мне/ }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Сохранить и проверить токен" }),
  );
  await waitFor(() => expect(mutations).toHaveLength(1));
  expect(mutations[0].body.expected_revision).toBe(2);
  expect(mutations[0].body.account).toBe(scope.account);
  expect(input.value).toBe("");
  expect(document.documentElement.innerHTML).not.toContain(
    "synthetic-replacement",
  );
  expect(JSON.stringify(localStorage)).not.toContain("synthetic-replacement");
  expect(JSON.stringify(sessionStorage)).not.toContain("synthetic-replacement");
});
it("uses a real probe request and explains authorization errors without starting imports", async () => {
  const { mutations } = fixture({ probeFailure: true });
  await screen.findByLabelText(/Название аккаунта/);
  fireEvent.click(screen.getByRole("button", { name: "Подключение" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Проверить подключение",
    }),
  );
  await screen.findByText(/МойСклад отклонил токен/);
  expect(mutations.map((m) => m.url)).toEqual([
    "/api/integrations/moysklad/probe",
  ]);
});
it("requires explicit disconnect confirmation and preserves history", async () => {
  const { mutations } = fixture();
  await screen.findByLabelText(/Название аккаунта/);
  fireEvent.click(screen.getByRole("button", { name: "Подключение" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Отключить аккаунт",
    }),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Подтвердить отключение",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(mutations).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Подтверждаю отключение аккаунта" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить отключение" }),
  );
  await waitFor(() => expect(mutations).toHaveLength(1));
  expect(mutations[0].url).toContain("/disconnect");
  expect(mutations[0].body.acknowledge).toBe(true);
});
it("binds the gear to the current MoySklad account", async () => {
  fixture();
  await screen.findByLabelText(/Название аккаунта/);
  expect(
    screen
      .getByRole("link", { name: "Настройки аккаунта МойСклад" })
      .getAttribute("href"),
  ).toBe("/integrations/moysklad/settings?account=moy-a");
});

it("keeps token replacement available when descriptive metadata cannot load", async () => {
  fixture({ metadataFailure: true });
  fireEvent.click(
    await screen.findByRole("button", { name: "Перейти к замене токена" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Заменить токен" }),
  );
  expect(screen.getByLabelText("Новый API-токен")).toBeTruthy();
});
