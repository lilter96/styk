import { useContext, useEffect } from "react";
import { UNSAFE_NavigationContext } from "react-router-dom";

// BrowserRouter and embedded component tests share this guard. Native dialogs
// remain keyboard accessible; section-only navigation never discards the draft.
export function useOrdersDraftGuard(pending: boolean) {
  const navigation = useContext(UNSAFE_NavigationContext);
  useEffect(() => {
    if (!pending) return;
    const question =
      "Есть несохранённые изменения или незавершённый запрос. Покинуть настройки?";
    let clickApproved = false;
    const scopeChange = (event: Event) => {
      if (!window.confirm(question)) event.preventDefault();
    };
    const unload = (event: BeforeUnloadEvent) => {
      if (clickApproved) return;
      event.preventDefault();
      event.returnValue = "";
    };
    const click = (event: MouseEvent) => {
      const link =
        event.target instanceof Element
          ? event.target.closest("a[href]")
          : null;
      if (
        !(link instanceof HTMLAnchorElement) ||
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey ||
        link.target === "_blank" ||
        link.download
      )
        return;
      const target = new URL(link.href);
      if (target.pathname === window.location.pathname) return;
      if (!window.confirm(question)) {
        event.preventDefault();
        event.stopPropagation();
        return;
      }
      clickApproved = true;
      queueMicrotask(() => {
        clickApproved = false;
      });
    };
    const originalURL = window.location.href;
    const pop = () => {
      if (window.location.pathname === new URL(originalURL).pathname) return;
      if (!window.confirm(question)) {
        window.history.pushState(null, "", originalURL);
        window.dispatchEvent(new PopStateEvent("popstate"));
      }
    };
    const navigator = navigation?.navigator;
    const originalPush = navigator?.push;
    const originalReplace = navigator?.replace;
    if (navigator && originalPush && originalReplace) {
      const guarded =
        (method: typeof originalPush): typeof originalPush =>
        (to, ...args) => {
          const pathname =
            typeof to === "string"
              ? new URL(to, window.location.href).pathname
              : to.pathname;
          if (
            clickApproved ||
            !pathname ||
            pathname === window.location.pathname ||
            window.confirm(question)
          )
            method.call(navigator, to, ...args);
        };
      navigator.push = guarded(originalPush);
      navigator.replace = guarded(originalReplace);
    }
    window.addEventListener("styk-before-scope-change", scopeChange);
    window.addEventListener("beforeunload", unload);
    document.addEventListener("click", click, true);
    // Capture before React Router's listener so a declined Back keeps the draft.
    window.addEventListener("popstate", pop, true);
    return () => {
      window.removeEventListener("styk-before-scope-change", scopeChange);
      window.removeEventListener("beforeunload", unload);
      document.removeEventListener("click", click, true);
      window.removeEventListener("popstate", pop, true);
      if (navigator && originalPush && originalReplace) {
        navigator.push = originalPush;
        navigator.replace = originalReplace;
      }
    };
  }, [pending, navigation]);
}
