import { useEffect, useState, type FormEvent } from "react";
import { authFetch, setCSRF } from "./api";

type Screen = "login" | "register" | "forgot" | "resend" | "verify" | "reset";

function fragment() {
  const value = new URLSearchParams(window.location.hash.slice(1));
  const verify = value.get("verify") || "";
  const reset = value.get("reset") || "";
  const token = verify || reset;
  if (!/^[A-Za-z0-9_-]{43}$/.test(token))
    return { screen: "login" as Screen, token: "" };
  return { screen: (verify ? "verify" : "reset") as Screen, token };
}

export function MerchantAuth({ onDone }: { onDone: () => void }) {
  const [initial] = useState(fragment);
  const [screen, setScreen] = useState<Screen>(initial.screen);
  const [token, setToken] = useState(initial.token);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [workspace, setWorkspace] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    setCSRF("");
    if (initial.token)
      window.history.replaceState(
        null,
        "",
        window.location.pathname + window.location.search,
      );
  }, [initial.token]);

  function move(next: Screen) {
    setScreen(next);
    setMessage("");
    setError("");
    setPassword("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const { path, body } = requestFor(
        screen,
        email,
        password,
        workspace,
        token,
      );
      const response = await authFetch(path, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(30000),
      });
      if (!response.ok) {
        throw new Error(
          response.status === 429
            ? "Слишком много попыток. Попробуйте позже."
            : screen === "login"
              ? "Не удалось войти. Проверьте почту, пароль и подтверждение адреса."
              : "Не удалось выполнить запрос. Проверьте данные или попробуйте позже.",
        );
      }
      setPassword("");
      if (screen === "login") {
        onDone();
      } else if (screen === "verify" || screen === "reset") {
        setToken("");
        setScreen("login");
        setMessage(
          screen === "verify"
            ? "Почта подтверждена. Теперь можно войти."
            : "Пароль изменён. Войдите с новым паролем.",
        );
      } else {
        setMessage(
          "Если адрес подходит, мы отправим письмо с дальнейшими инструкциями.",
        );
      }
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Не удалось выполнить запрос.",
      );
    } finally {
      setBusy(false);
    }
  }

  const titles: Record<Screen, string> = {
    login: "Войти в STYK",
    register: "Создать аккаунт",
    forgot: "Восстановить пароль",
    resend: "Подтвердить почту",
    verify: "Подтвердить почту",
    reset: "Новый пароль",
  };
  const buttons: Record<Screen, string> = {
    login: "Войти",
    register: "Создать аккаунт",
    forgot: "Отправить письмо",
    resend: "Отправить письмо",
    verify: "Подтвердить адрес",
    reset: "Сохранить пароль",
  };
  return (
    <main className="login">
      <div className="login-mark">
        S<span>TYK</span>
      </div>
      <div className="login-panel merchant-login-panel">
        <h1>{titles[screen]}</h1>
        <p>
          {screen === "register"
            ? "Создайте пространство для своего магазина."
            : screen === "login"
              ? "Управляйте остатками своего магазина в одном месте."
              : screen === "verify"
                ? "Подтвердите адрес и задайте пароль для входа. Ранее указанный пароль будет заменён."
                : "Используйте адрес, указанный при регистрации."}
        </p>
        <form onSubmit={(event) => void submit(event)}>
          {screen !== "verify" && screen !== "reset" && (
            <>
              <label htmlFor="merchant-email">Электронная почта</label>
              <input
                id="merchant-email"
                type="email"
                autoComplete="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
            </>
          )}
          {(screen === "login" ||
            screen === "register" ||
            screen === "verify" ||
            screen === "reset") && (
            <>
              <label htmlFor="merchant-password">
                {screen === "reset" || screen === "verify"
                  ? "Новый пароль"
                  : "Пароль"}
              </label>
              <input
                id="merchant-password"
                type="password"
                autoComplete={
                  screen === "login" ? "current-password" : "new-password"
                }
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                minLength={screen === "login" ? undefined : 12}
                required
              />
              {screen !== "login" && (
                <small>Не менее 12 символов, максимум 72 байта.</small>
              )}
            </>
          )}
          {screen === "register" && (
            <>
              <label htmlFor="merchant-workspace">Название магазина</label>
              <input
                id="merchant-workspace"
                value={workspace}
                onChange={(event) => setWorkspace(event.target.value)}
                maxLength={120}
                required
              />
            </>
          )}
          <button disabled={busy}>
            {busy ? "Подождите…" : buttons[screen]}
          </button>
          {message && (
            <p className="merchant-auth-message" role="status">
              {message}
            </p>
          )}
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
        </form>
        <div className="merchant-auth-links">
          {screen === "login" ? (
            <>
              <button
                type="button"
                className="text-action"
                onClick={() => move("forgot")}
              >
                Забыли пароль?
              </button>
              <button
                type="button"
                className="text-action"
                onClick={() => move("register")}
              >
                Создать аккаунт
              </button>
              <button
                type="button"
                className="text-action"
                onClick={() => move("resend")}
              >
                Отправить подтверждение ещё раз
              </button>
            </>
          ) : (
            <button
              type="button"
              className="text-action"
              onClick={() => move("login")}
            >
              Вернуться ко входу
            </button>
          )}
        </div>
      </div>
      <p className="login-footer">STYK · Рабочее пространство магазина</p>
    </main>
  );
}

function requestFor(
  screen: Screen,
  email: string,
  password: string,
  workspace: string,
  token: string,
) {
  switch (screen) {
    case "login":
      return { path: "/api/session", body: { email, password } };
    case "register":
      return {
        path: "/api/registration",
        body: { email, password, workspace },
      };
    case "forgot":
      return { path: "/api/password-reset/request", body: { email } };
    case "resend":
      return { path: "/api/verification/request", body: { email } };
    case "verify":
      return { path: "/api/verification", body: { token, password } };
    case "reset":
      return { path: "/api/password-reset", body: { token, password } };
  }
}
