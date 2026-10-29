import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { CredentialEditor } from "./CredentialEditor";
import {
  CredentialNotice,
  useCredentialSubmission,
  type CredentialMetadata,
  type CredentialScope,
} from "./CredentialSubmission";
const original: CredentialScope = {
  tenant: "credential-tenant",
  provider: "moysklad",
  account: "original-account",
};
const other: CredentialScope = {
  tenant: "other-tenant",
  provider: "ozon",
  account: "other-account",
};
const typedSecret = "synthetic-password-never-rendered";
type Body = {
  tenant: string;
  account: string;
  name?: string;
  secret?: string;
  request_id: string;
  expected_revision: number;
  acknowledge: boolean;
};
function metadata(
  scope = original,
  revision = 4,
  requestID = "previous",
): CredentialMetadata {
  return {
    ...scope,
    name: "Основной склад",
    revision,
    enabled: true,
    checked_at: "2026-10-29T06:35:00Z",
    updated_at: "2026-10-29T06:35:00Z",
    last_request_id: requestID,
  };
}
function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}
function Harness({
  registration = "REGISTERED",
  reason,
}: { registration?: string; reason?: string } = {}) {
  const submission = useCredentialSubmission();
  const [scope, setScope] = useState(original);
  const [open, setOpen] = useState(true);
  return (
    <>
      <button onClick={() => setScope(other)}>Другой аккаунт и сервис</button>
      <CredentialNotice
        submission={submission}
        tenant={scope.tenant}
        restore={(scope) => {
          setScope(scope);
          setOpen(true);
        }}
      />
      {open && (
        <section aria-label="Редактор подключения">
          <CredentialEditor
            key={`${scope.tenant}:${scope.provider}:${scope.account}`}
            scope={scope}
            registration={registration}
            reason={reason}
            name="Основной склад"
            accounts={[
              {
                source: scope.provider,
                account: scope.account,
                name: "Основной склад",
                registered_at: "2026-10-29T06:35:00Z",
              },
            ]}
            onSelect={() => {}}
            submission={submission}
            onClose={() => setOpen(false)}
          />
        </section>
      )}
    </>
  );
}
function mockAPI(
  options: {
    lost?: boolean;
    rejection?: string;
    wrong?: "scope" | "revision";
    latestForEmpty?: boolean;
    unavailable?: boolean;
  } = {},
) {
  const posts: { path: string; text: string; body: Body }[] = [],
    gets: string[] = [];
  const key = (scope: CredentialScope) =>
    `${scope.tenant}:${scope.provider}:${scope.account}`;
  const records = new Map([
    [key(original), metadata()],
    [key(other), metadata(other)],
  ]);
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const path = String(input),
      url = new URL(path, "http://127.0.0.1"),
      provider = path.includes("/moysklad/") ? "moysklad" : "ozon";
    if (init?.method === "POST") {
      const text = String(init.body),
        body = JSON.parse(text) as Body;
      posts.push({ path, text, body });
      if (!path.includes("/credentials") && !path.endsWith("/disconnect"))
        throw new Error("unexpected mutation");
      if (options.rejection)
        return new Response(JSON.stringify({ error: options.rejection }), {
          status: options.rejection === "PROBE_TIMEOUT" ? 422 : 409,
        });
      const scope: CredentialScope = {
        tenant: body.tenant,
        provider,
        account: body.account || "derived-moy-account",
      };
      const saved = {
        ...metadata(scope, body.expected_revision + 1, body.request_id),
        name: body.name || "Основной склад",
        enabled: !path.endsWith("/disconnect"),
      };
      records.set(key(scope), saved);
      if (options.lost && posts.length === 1)
        throw new TypeError("synthetic response lost");
      if (options.wrong === "scope") saved.account = "unexpected-account";
      if (options.wrong === "revision") saved.revision += 20;
      return new Response(
        JSON.stringify({
          credential: saved,
          idempotent_replay: posts.length > 1,
        }),
        { status: 200 },
      );
    }
    gets.push(path);
    const scope: CredentialScope = {
      tenant: url.searchParams.get("tenant") || "",
      provider,
      account: url.searchParams.get("account") || "",
    };
    return new Response(
      JSON.stringify({
        storage_available: !options.unavailable,
        credential:
          records.get(key(scope)) ||
          (options.latestForEmpty && !scope.account
            ? metadata(original)
            : null),
      }),
      { status: 200 },
    );
  });
  return { posts, gets };
}
const editor = () =>
  within(screen.getByRole("region", { name: "Редактор подключения" }));
async function fillAndSave() {
  const field = await editor().findByLabelText("Новый ключ доступа");
  fireEvent.change(field, { target: { value: typedSecret } });
  const button = editor().getByRole("button", {
    name: "Сохранить и проверить",
  });
  expect((button as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(editor().getByLabelText(/Подтверждаю, что этот аккаунт/));
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(button);
}
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
it("keeps password out of serialized HTML/storage and saves only after explicit acknowledgement", async () => {
  const { posts } = mockAPI();
  const view = render(<Harness />, { wrapper: wrapper() });
  const password = await editor().findByLabelText("Новый ключ доступа");
  fireEvent.change(password, { target: { value: typedSecret } });
  expect((password as HTMLInputElement).value).toBe(typedSecret);
  expect(view.container.innerHTML).not.toContain(typedSecret);
  expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain(
    typedSecret,
  );
  expect(posts).toHaveLength(0);
  expect(
    (
      editor().getByRole("button", {
        name: "Сохранить и проверить",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(editor().getByLabelText(/Подтверждаю, что этот аккаунт/));
  fireEvent.click(
    editor().getByRole("button", { name: "Сохранить и проверить" }),
  );
  await editor().findByText("Ключ сохранён, подключение проверено.");
  expect(posts).toHaveLength(1);
  expect(posts[0].body).toMatchObject({
    tenant: original.tenant,
    account: original.account,
    secret: typedSecret,
    name: "Основной склад",
    expected_revision: 4,
    acknowledge: true,
  });
  expect(posts[0].body.request_id).toMatch(/^[0-9a-f-]{36}$/);
  expect((password as HTMLInputElement).value).toBe("");
  expect(posts.every((post) => !post.path.includes("/api/imports"))).toBe(true);
  expect(view.container.innerHTML).not.toContain(typedSecret);
});
it("retains one original request across closing/settings account changes and retries its exact body", async () => {
  const { posts } = mockAPI({ lost: true });
  const view = render(<Harness />, { wrapper: wrapper() });
  await fillAndSave();
  await editor().findByText(/Результат сохранения неизвестен/);
  expect(
    (editor().getByLabelText("Новый ключ доступа") as HTMLInputElement).value,
  ).toBe("");
  expect(view.container.innerHTML).not.toContain(typedSecret);
  fireEvent.click(editor().getByRole("button", { name: "Отмена" }));
  expect(
    screen.queryByRole("region", { name: "Редактор подключения" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Другой аккаунт и сервис" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "К исходному подключению" }),
  );
  await editor().findByLabelText("Новый ключ доступа");
  fireEvent.click(
    screen.getAllByRole("button", { name: "Повторить тот же запрос" })[0],
  );
  await editor().findByText(/Результат прежнего запроса найден/);
  expect(posts).toHaveLength(2);
  expect(posts[1].path).toBe(posts[0].path);
  expect(posts[1].text).toBe(posts[0].text);
  expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain(
    typedSecret,
  );
});
it("blocks new provider mutations while the original credential outcome is uncertain", async () => {
  const { posts } = mockAPI({ lost: true });
  const { result } = renderHook(() => useCredentialSubmission(), {
    wrapper: wrapper(),
  });
  act(() =>
    result.current.start(original, {
      name: "Original",
      secret: typedSecret,
      expectedRevision: 4,
    }),
  );
  await waitFor(() => expect(result.current.pending?.uncertain).toBe(true));
  act(() => {
    result.current.start(other, {
      name: "Other",
      secret: "another-synthetic-key",
      expectedRevision: 4,
    });
    result.current.disconnect(other, 4);
  });
  expect(posts).toHaveLength(1);
  act(() => result.current.retry());
  await waitFor(() => expect(result.current.pending).toBeNull());
  expect(posts).toHaveLength(2);
  expect(posts[1].text).toBe(posts[0].text);
});
it("reconciles a committed lost response through original scoped metadata and request ID", async () => {
  const { posts, gets } = mockAPI({ lost: true });
  const { result } = renderHook(() => useCredentialSubmission(), {
    wrapper: wrapper(),
  });
  act(() =>
    result.current.start(original, {
      name: "Original",
      secret: typedSecret,
      expectedRevision: 4,
    }),
  );
  await waitFor(() => expect(result.current.pending?.uncertain).toBe(true));
  await act(async () => result.current.inspect());
  expect(result.current.pending).toBeNull();
  expect(result.current.result?.revision).toBe(5);
  expect(result.current.result?.last_request_id).toBe(posts[0].body.request_id);
  expect(gets[0]).toContain(`tenant=${original.tenant}`);
  expect(gets[0]).toContain(`account=${original.account}`);
  expect(posts).toHaveLength(1);
});
for (const [code, message] of [
  ["INVALID_CREDENTIALS", "Сервис не принял ключ"],
  ["CREDENTIAL_VERSION_CONFLICT", "Настройки уже изменились"],
  ["PROBE_TIMEOUT", "Сервис не ответил вовремя"],
] as const)
  it(`reports definitive ${code} without retaining an uncertain write`, async () => {
    const { posts } = mockAPI({ rejection: code });
    const { result } = renderHook(() => useCredentialSubmission(), {
      wrapper: wrapper(),
    });
    act(() =>
      result.current.start(original, {
        name: "Original",
        secret: typedSecret,
        expectedRevision: 4,
      }),
    );
    await waitFor(() => expect(result.current.busy).toBe(false));
    expect(result.current.pending).toBeNull();
    expect(result.current.message).toContain(message);
    expect(posts).toHaveLength(1);
  });
for (const wrong of ["scope", "revision"] as const)
  it(`does not accept a ${wrong} mismatch as a completed credential save`, async () => {
    mockAPI({ wrong });
    const { result } = renderHook(() => useCredentialSubmission(), {
      wrapper: wrapper(),
    });
    act(() =>
      result.current.start(original, {
        name: "Original",
        secret: typedSecret,
        expectedRevision: 4,
      }),
    );
    await waitFor(() => expect(result.current.busy).toBe(false));
    expect(result.current.result).toBeNull();
    expect(result.current.pending?.uncertain).toBe(true);
  });
it("requires a fresh metadata inspection before dismissing an idempotency conflict", async () => {
  const { posts, gets } = mockAPI({ rejection: "IDEMPOTENCY_CONFLICT" });
  const { result } = renderHook(() => useCredentialSubmission(), {
    wrapper: wrapper(),
  });
  act(() =>
    result.current.start(original, {
      name: "Original",
      secret: typedSecret,
      expectedRevision: 4,
    }),
  );
  await waitFor(() => expect(result.current.pending?.uncertain).toBe(true));
  act(() => {
    result.current.dismiss();
    result.current.retry();
  });
  expect(posts).toHaveLength(1);
  expect(result.current.pending).not.toBeNull();
  await act(async () => result.current.inspect());
  expect(result.current.inspected).toBe(true);
  expect(gets).toHaveLength(1);
  act(() => result.current.dismiss());
  expect(result.current.pending).toBeNull();
  expect(posts).toHaveLength(1);
});
it("disconnects only after separate acknowledgement, with exact version and no residual typed key", async () => {
  const { posts } = mockAPI();
  render(<Harness />, { wrapper: wrapper() });
  const password = await editor().findByLabelText("Новый ключ доступа");
  fireEvent.change(password, { target: { value: typedSecret } });
  fireEvent.click(editor().getByRole("button", { name: "Отключить МойСклад" }));
  const confirm = editor().getByRole("button", {
    name: "Подтвердить отключение",
  });
  expect((confirm as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(editor().getByLabelText("Подтверждаю отключение МойСклад"));
  fireEvent.click(confirm);
  await editor().findByText(/Подключение отключено/);
  expect(posts).toHaveLength(1);
  expect(posts[0].path).toBe("/api/integrations/moysklad/disconnect");
  expect(posts[0].body).toMatchObject({
    tenant: original.tenant,
    account: original.account,
    expected_revision: 4,
    acknowledge: true,
  });
  expect(posts[0].body.secret).toBeUndefined();
  expect((password as HTMLInputElement).value).toBe("");
});

it("creates a new MoySklad account at revision zero even when empty-account metadata lists an older connection", async () => {
  const { posts } = mockAPI({ latestForEmpty: true });
  const onSelect = vi.fn();
  function NewConnection() {
    const submission = useCredentialSubmission();
    return (
      <section aria-label="Редактор подключения">
        <CredentialEditor
          scope={{ ...original, account: "" }}
          name="Новый склад"
          registration="NOT_REGISTERED"
          accounts={[
            {
              source: "moysklad",
              account: original.account,
              registered_at: "2026-10-29T06:35:00Z",
            },
          ]}
          onSelect={onSelect}
          submission={submission}
          onClose={() => {}}
        />
      </section>
    );
  }
  render(<NewConnection />, { wrapper: wrapper() });
  const password = await editor().findByLabelText("Ключ доступа");
  expect(
    (editor().getByLabelText("Название подключения") as HTMLInputElement).value,
  ).toBe("Новый склад");
  fireEvent.change(password, { target: { value: typedSecret } });
  fireEvent.click(editor().getByLabelText(/Подтверждаю, что этот аккаунт/));
  const save = editor().getByRole("button", { name: "Сохранить и проверить" });
  await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(save);
  await editor().findByText("Ключ сохранён, подключение проверено.");
  expect(posts).toHaveLength(1);
  expect(posts[0].body).toMatchObject({
    account: "",
    expected_revision: 0,
    name: "Новый склад",
  });
  expect(onSelect).not.toHaveBeenCalled();
  expect((password as HTMLInputElement).value).toBe("");
});
it("cannot submit a secret when encrypted storage is unavailable", async () => {
  const { posts } = mockAPI({ unavailable: true });
  render(<Harness />, { wrapper: wrapper() });
  await editor().findByText(/Сохранение ключей пока не настроено/);
  fireEvent.change(editor().getByLabelText("Новый ключ доступа"), {
    target: { value: typedSecret },
  });
  fireEvent.click(editor().getByLabelText(/Подтверждаю, что этот аккаунт/));
  expect(
    (
      editor().getByRole("button", {
        name: "Сохранить и проверить",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(posts).toHaveLength(0);
});

for (const reason of ["INVALID_CREDENTIALS", "PERMISSION_DENIED", "TIMEOUT"])
  it(`describes failed verification accurately for ${reason}`, async () => {
    const { posts } = mockAPI();
    render(<Harness registration="VERIFICATION_FAILED" reason={reason} />, {
      wrapper: wrapper(),
    });
    await editor().findByLabelText("Новый ключ доступа");
    if (reason === "INVALID_CREDENTIALS") {
      expect(editor().getByText("Ключ не принят.")).toBeTruthy();
      expect(editor().queryByText("Не удалось проверить доступ.")).toBeNull();
    } else {
      expect(editor().getByText("Не удалось проверить доступ.")).toBeTruthy();
      expect(editor().queryByText("Ключ не принят.")).toBeNull();
    }
    expect(posts).toHaveLength(0);
  });
