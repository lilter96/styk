import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { BrandLogo } from "./BrandLogo";
import { brandRegistry, integrationBrand, type BrandProvider } from "./brands";

afterEach(cleanup);
describe("brand artwork", () => {
  it.each(Object.entries(brandRegistry))(
    "labels %s and uses its canonical artwork",
    (provider, asset) => {
      const { container } = render(
        <BrandLogo provider={provider as BrandProvider} />,
      );
      expect(screen.getByRole("img", { name: asset.label })).toBeTruthy();
      const image = container.querySelector("img");
      if (asset.src) expect(image?.getAttribute("src")).toBe(asset.src);
      else expect(container.textContent).toBe("S");
    },
  );
  it("hides decorative artwork from assistive technology", () => {
    render(<BrandLogo provider="ozon" decorative />);
    expect(screen.queryByRole("img")).toBeNull();
  });
  it("falls back on an image error and recovers when the brand changes", () => {
    const { container, rerender } = render(
      <BrandLogo provider="ozon" className="context" size="xl" />,
    );
    fireEvent.error(container.querySelector("img")!);
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByRole("img", { name: "Ozon" }).textContent).toBe("O");
    expect(container.firstElementChild?.classList.contains("context")).toBe(
      true,
    );
    rerender(<BrandLogo provider="moysklad" />);
    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      brandRegistry.moysklad.src,
    );
  });
  it("has an accessible fallback for an unexpected runtime provider", () => {
    render(<BrandLogo provider={"missing" as BrandProvider} />);
    expect(screen.getByRole("img", { name: "Интеграция" }).textContent).toBe(
      "?",
    );
  });
  it("maps existing Yandex IDs without advertising backend support", () => {
    expect(integrationBrand("yandex")).toBe("yandex-market");
  });
});
