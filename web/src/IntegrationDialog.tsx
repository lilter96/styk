import { useEffect, useId, useRef, type ReactNode } from "react";

const focusableSelector =
  "a[href], button, input, select, textarea, summary, [tabindex]";

function focusableElements(container: HTMLElement) {
  return Array.from(
    container.querySelectorAll<HTMLElement>(focusableSelector),
  ).filter((element) => {
    if (
      element.tabIndex < 0 ||
      element.matches(':disabled, input[type="hidden"]') ||
      element.closest('[hidden], [inert], [aria-hidden="true"]')
    )
      return false;
    for (
      let ancestor: HTMLElement | null = element;
      ancestor && ancestor !== container.parentElement;
      ancestor = ancestor.parentElement
    ) {
      const style = window.getComputedStyle(ancestor);
      if (style.display === "none" || style.visibility === "hidden")
        return false;
      if (ancestor instanceof HTMLDetailsElement && !ancestor.open) {
        const summary = ancestor.querySelector(":scope > summary");
        if (!summary?.contains(element)) return false;
      }
    }
    return true;
  });
}

export function IntegrationDialog({
  title,
  children,
  onClose,
  closeLabel = "Закрыть",
  className = "",
  initialFocus,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  closeLabel?: string;
  className?: string;
  initialFocus?: string;
}) {
  const titleID = useId();
  const dialog = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;

  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    const originalOverflow = document.body.style.overflow;
    const originalPaddingRight = document.body.style.paddingRight;
    const viewportWidth = document.documentElement.clientWidth;
    const scrollbarWidth =
      viewportWidth > 0 ? window.innerWidth - viewportWidth : 0;
    if (scrollbarWidth > 0) {
      const existingPadding =
        parseFloat(window.getComputedStyle(document.body).paddingRight) || 0;
      document.body.style.paddingRight = `${existingPadding + scrollbarWidth}px`;
    }
    document.body.style.overflow = "hidden";
    const currentDialog = dialog.current;
    if (content.current && currentDialog) {
      const firstControl =
        (initialFocus
          ? content.current.querySelector<HTMLElement>(initialFocus)
          : null) || focusableElements(content.current)[0];
      (
        firstControl ||
        focusableElements(currentDialog)[0] ||
        currentDialog
      ).focus();
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        close.current();
        return;
      }
      if (event.key !== "Tab" || !currentDialog) return;
      const controls = focusableElements(currentDialog);
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (!first || !last) {
        event.preventDefault();
        currentDialog.focus();
      } else if (
        event.shiftKey &&
        (document.activeElement === first ||
          !currentDialog.contains(document.activeElement))
      ) {
        event.preventDefault();
        last.focus();
      } else if (
        !event.shiftKey &&
        (document.activeElement === last ||
          !currentDialog.contains(document.activeElement))
      ) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.body.style.overflow = originalOverflow;
      document.body.style.paddingRight = originalPaddingRight;
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, []);

  return (
    <div
      className="provider-settings-overlay"
      onClick={(event) => {
        if (event.target === event.currentTarget) close.current();
      }}
    >
      <div
        ref={dialog}
        className={`provider-settings-dialog ${className}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        tabIndex={-1}
      >
        <header className="provider-settings-dialog-header">
          <h2 id={titleID}>{title}</h2>
          <button
            className="secondary"
            aria-label={closeLabel}
            onClick={() => close.current()}
          >
            <span aria-hidden="true">×</span>
          </button>
        </header>
        <div ref={content} className="provider-settings-dialog-content">
          {children}
        </div>
      </div>
    </div>
  );
}
