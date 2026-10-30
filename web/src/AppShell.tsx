import { useState, type ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { IntegrationDialog } from "./IntegrationDialog";
import { Brand, Icon, type IconName } from "./UI";

const navigation: { path: string; label: string; icon: IconName }[] = [
  { path: "/", label: "Обзор", icon: "overview" },
  { path: "/products", label: "Товары", icon: "products" },
  { path: "/catalog-sync", label: "Каталог", icon: "catalog" },
  { path: "/warehouses", label: "Склады", icon: "warehouse" },
  { path: "/integrations", label: "Интеграции", icon: "integrations" },
  { path: "/diagnostics", label: "Диагностика", icon: "diagnostics" },
];
function Navigation({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav className="app-tabs" aria-label="Разделы">
      {navigation.map((item) => (
        <NavLink
          key={item.path}
          to={item.path}
          end={item.path === "/"}
          onClick={onNavigate}
        >
          <Icon name={item.icon} />
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}
export function AppShell({
  context,
  workspace,
  children,
}: {
  context: (onNavigate?: () => void) => ReactNode;
  workspace: (onNavigate?: () => void) => ReactNode;
  children: ReactNode;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  return (
    <div className="shell reference-shell">
      <a className="skip-link" href="#workspace-content">
        Перейти к содержимому
      </a>
      <aside className="app-sidebar">
        {workspace()}
        <Navigation />
        <div className="app-sidebar-foot">
          <Brand />
          {context()}
        </div>
      </aside>
      <div className="mobile-app-bar">
        <button
          className="mobile-nav-toggle secondary"
          type="button"
          aria-expanded={menuOpen}
          aria-label="Открыть меню"
          onClick={() => setMenuOpen(true)}
        >
          <Icon name="menu" />
        </button>
      </div>
      {menuOpen && (
        <IntegrationDialog
          title="Разделы STYK"
          className="navigation-drawer"
          onClose={() => setMenuOpen(false)}
        >
          {workspace(() => setMenuOpen(false))}
          <Navigation onNavigate={() => setMenuOpen(false)} />
          <div className="app-sidebar-foot">
            <Brand />
            {context(() => setMenuOpen(false))}
          </div>
        </IntegrationDialog>
      )}
      <main className="main" id="workspace-content" tabIndex={-1}>
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
