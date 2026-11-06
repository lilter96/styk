import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { setCSRF } from "./api";
import { ProfileSettings } from "./ProfileSettings";
import type { MarketplacePlan } from "./marketplacePlan";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  setCSRF("");
});

const plan: MarketplacePlan = {
  code: "plan_3",
  total_limit: 5,
  provider_limit: 5,
  revision: 1,
  used: 2,
  providers: { ozon: 1, wildberries: 1 },
  exceeded: false,
  blocked: {},
};

function fillPasswordForm() {
  fireEvent.change(screen.getByLabelText("Текущий пароль"), {
    target: { value: "current-password" },
  });
  fireEvent.change(screen.getByLabelText("Новый пароль"), {
    target: { value: "replacement-password" },
  });
  fireEvent.change(screen.getByLabelText("Повторите новый пароль"), {
    target: { value: "replacement-password" },
  });
}

it("shows the account plan and marketplace usage", () => {
  render(
    <ProfileSettings email="seller@example.test" plan={plan} />,
  );
  expect(
    screen.getByRole("heading", { name: "Настройки профиля" }),
  ).toBeTruthy();
  expect(screen.getByText("seller@example.test")).toBeTruthy();
  expect(screen.getByText("Тариф 3")).toBeTruthy();
  expect(screen.getByText("2 из 5")).toBeTruthy();
  expect(screen.getByText("Ozon")).toBeTruthy();
  expect(screen.getByText("Wildberries")).toBeTruthy();
});

it("changes the password without retaining entered secrets", async () => {
  const fetch = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    expect(String(_input)).toBe("/api/profile/password");
    expect(JSON.parse(String(init?.body))).toEqual({
      current_password: "current-password",
      new_password: "replacement-password",
    });
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal("fetch", fetch);
  render(<ProfileSettings email="seller@example.test" plan={plan} />);
  fillPasswordForm();
  fireEvent.click(screen.getByRole("button", { name: "Сохранить пароль" }));
  expect((await screen.findByRole("status")).textContent).toContain(
    "Пароль изменён",
  );
  expect(
    (screen.getByLabelText("Текущий пароль") as HTMLInputElement).value,
  ).toBe("");
  expect(
    (screen.getByLabelText("Новый пароль") as HTMLInputElement).value,
  ).toBe("");
  expect(
    (screen.getByLabelText("Повторите новый пароль") as HTMLInputElement).value,
  ).toBe("");
});

it("reports an incorrect current password", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "INVALID_CURRENT_PASSWORD" }), {
          status: 400,
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );
  render(<ProfileSettings email="seller@example.test" plan={plan} />);
  fillPasswordForm();
  fireEvent.click(screen.getByRole("button", { name: "Сохранить пароль" }));
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Текущий пароль указан неверно.",
  );
});

it("keeps password changes unavailable in support mode", () => {
  render(<ProfileSettings email="seller@example.test" plan={plan} readOnly />);
  expect(screen.queryByLabelText("Текущий пароль")).toBeNull();
  expect(screen.getByText(/режиме поддержки/)).toBeTruthy();
});
