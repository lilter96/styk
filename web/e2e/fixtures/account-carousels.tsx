// Explicit browser-only geometry fixture. No API requests or production records.
import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { IntegrationProviderSection } from "../../src/IntegrationProviderSection";
import "../../src/style.css";
import "../../src/design-system.css";
import "../../src/integrations-page.css";
function Fixture() {
  const [count, setCount] = useState(10);
  return <div className="reference-shell" style={{ padding: 20 }}><main className="integration-console"><label>Fixture account count<select aria-label="Fixture account count" value={count} onChange={(event) => setCount(Number(event.target.value))}>{[1,3,5,10,20].map((value) => <option key={value}>{value}</option>)}</select></label>{(["ozon", "moysklad"] as const).map((provider) => <IntegrationProviderSection key={provider} provider={provider} onAdd={() => {}} items={Array.from({ length: count }, (_, index) => ({ id: `${provider}-${index}`, name: `Account ${index + 1}`, content: <article className="integration-account-card"><header className="account-card-header"><h3>Account {index + 1}</h3><span className="connection-state">Synthetic fixture</span></header><p>Isolated UI geometry test</p><button className="secondary">Settings {index + 1}</button></article> }))} />)}</main></div>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
