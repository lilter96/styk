import { expect, type Locator, type Page } from "@playwright/test";

async function labelContrast(button: Locator) {
  return button.evaluate(async (element) => {
    // Flush the hover style, then wait for its transitions rather than sampling
    // the first frame (which would conceal the original cascade regression).
    void window.getComputedStyle(element).backgroundColor;
    await Promise.all(
      element
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    );
    type Color = [number, number, number, number];
    const parse = (value: string): Color => {
      const parts = value.match(/[\d.]+/g)?.map(Number) ?? [];
      return [parts[0] ?? 0, parts[1] ?? 0, parts[2] ?? 0, parts[3] ?? 1];
    };
    const over = (front: Color, back: Color): Color =>
      [
        ...front
          .slice(0, 3)
          .map(
            (channel, index) =>
              channel * front[3] + back[index] * (1 - front[3]),
          ),
        1,
      ] as Color;
    const luminance = (color: Color) =>
      color.slice(0, 3).reduce((sum, channel, index) => {
        const value = channel / 255;
        return (
          sum +
          (value <= 0.04045
            ? value / 12.92
            : ((value + 0.055) / 1.055) ** 2.4) *
            [0.2126, 0.7152, 0.0722][index]
        );
      }, 0);
    const backgrounds = (target: Element): Color[] => {
      const ancestors: Element[] = [];
      for (let node: Element | null = target; node; node = node.parentElement)
        ancestors.unshift(node);
      return ancestors.reduce<Color[]>(
        (colors, ancestor) => {
          const style = window.getComputedStyle(ancestor);
          const surfaces = (
            style.backgroundImage.match(/rgba?\([^)]+\)/g) ?? []
          ).map(parse);
          if (!surfaces.length) surfaces.push(parse(style.backgroundColor));
          return colors.flatMap((back) =>
            surfaces.map((front) => over(front, back)),
          );
        },
        [[255, 255, 255, 1]],
      );
    };
    const labels = new Set<Element>(element.querySelectorAll("svg"));
    const walker = document.createTreeWalker(element, 4);
    while (walker.nextNode()) {
      const parent = walker.currentNode.parentElement;
      if (
        walker.currentNode.textContent?.trim() &&
        parent?.getClientRects().length
      )
        labels.add(parent);
    }
    const ratios = Array.from(labels).flatMap((label) => {
      const foreground = parse(window.getComputedStyle(label).color);
      return backgrounds(label).map((background) => {
        const front = luminance(over(foreground, background));
        const back = luminance(background);
        return (Math.max(front, back) + 0.05) / (Math.min(front, back) + 0.05);
      });
    });
    return {
      ratio: ratios.length ? Math.min(...ratios) : null,
      label: element.getAttribute("aria-label") || element.textContent?.trim(),
      classes: element.className,
    };
  });
}

export async function auditButtonHovers(page: Page, area: string) {
  const results = [];
  const dialogs = page.getByRole("dialog");
  const region = (await dialogs.count()) ? dialogs.last() : page;
  for (const button of await region.locator("button:visible").all()) {
    if (!(await button.isEnabled())) continue;
    await page.mouse.move(0, 0);
    const normal = await labelContrast(button);
    if (normal.ratio === null) continue;
    await button.hover();
    const hovered = await labelContrast(button);
    // Preserve existing contrast, up to the normal-text AA threshold. This
    // catches disappearing hover labels without asserting unrelated rest styles.
    const minimum = Math.min(normal.ratio, 4.5) - 0.15;
    expect(
      hovered.ratio,
      `${area}: ${normal.label} (${normal.classes}), normal=${normal.ratio.toFixed(2)}`,
    ).toBeGreaterThanOrEqual(minimum);
    results.push({
      label: normal.label,
      normal: normal.ratio,
      hover: hovered.ratio,
    });
  }
  await page.mouse.move(0, 0);
  return results;
}
