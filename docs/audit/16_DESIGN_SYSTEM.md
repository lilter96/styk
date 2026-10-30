# Дизайн-система: фактическое состояние

## Источники

Рабочая консоль: web/src/style.css, imports.css, mapping.css, design-system.css, catalog-sync.css; AppShell.tsx, UI.tsx, IntegrationDialog.tsx, WorkspaceSwitcher.tsx. Landing: соседний src/style.css, connection-flow.css, main.tsx, ConnectionScene.tsx. Demo: src/style.css, main.tsx, Hero3D.tsx.

Это наблюдаемая кодовая система, не подтверждённая Figma library или Code Connect mapping. Исходные design files в аудит не предоставлены.

## Фирменный язык

CONFIRMED console tokens:

| Token | Значение | Назначение |
|---|---|---|
| --styk-primary | #693cff | primary action/active |
| --styk-primary-hover | #5930e8 | hover |
| --styk-blue | #1769ff | secondary brand/links |
| --styk-ink | #111936 | основной текст |
| --styk-muted | #667394 | secondary text |
| --styk-line | #e9edf8 | separators |
| --styk-background | #f6f8ff | page background |
| --styk-gradient | purple #693cff → #9254ff | active/brand |
| --styk-success | #12b981 | success |
| --styk-warning | #ffb020 | warning |
| --styk-danger | #ff4567 | error |
| radius sm/md/lg | 8/12/16 px | controls/cards/dialogs |

Soft/ink/line variants для status добавлены рядом. Токены --ink/--muted/--line/--accent перенаправлены на foundations. Цвета и sizes в остальных CSS остаются частично literal; это не fully tokenized design system.

Inter — общий sans-serif язык landing/demo; console имеет local font assets и monospace JetBrains для технических значений. Landing/demo CSS импортируют Google Fonts, что отличается по privacy/availability от локальных console fonts.

Логотипы различаются: AppShell/UI Brand использует стилизованный S + TYK; landing Logo — grid/mark; demo Logo — star mark. Это visual family, но не единый shared package. Provider logos хранятся как PNG/SVG assets; права и правила использования требуют отдельной проверки.

## Layout

Console: fixed sidebar 222px, белые panels на светлом фоне, purple active nav, main content max-width 1600px, adaptive mobile top bar и navigation drawer. AppShell содержит skip link и semantic navigation/main. Landing: большие sections, hero dashboard mockup, rounded cards, pill badges и feature illustrations. Demo: full application sidebar, chart/metric cards, tables/details/modals.

Responsive есть в @media, но handset/tablet QA, zoom, contrast и screen reader проходы не выполнялись. Нельзя утверждать WCAG соответствие только по наличию aria labels.

## Компоненты

UI.tsx: Brand/Icon/PageHeader/StatePanel; IconName union и SVG paths без отдельного icon package в рабочем app. AppShell: navigation/sidebar/mobile dialog. IntegrationDialog — shared modal boundary. WorkspaceSwitcher — provider/account scope selection. ProductMapping и WarehouseReview имеют специализированные queue/table/confirmation состояния.

Server loading/error/empty передаются StatePanel; mutation notices выделяют pending/unknown/session lost. Это важная часть interaction system: success toast не должен скрывать uncertain remote write. Tables обычные HTML, не enterprise data-grid. Формы native inputs + React state + server validation.

## Анимация и 3D

- Console: CSS transitions/responsive UI, Three.js dependencies в web/package.json нет.
- Demo: motion + MotionConfig reducedMotion=user, Recharts, lazy Hero3D Canvas, react-three/fiber/drei/three.
- Demo Hero3D: procedural geometries/frame rotation; external GLB/GLTF loading в просмотренном src/public не найдено.
- Landing ConnectionScene: SVG paths/animateMotion, hover/active node, IntersectionObserver; не Three.js сцена.
- Landing charts: static SVG, demo numbers; demo charts: Recharts synthetic series.

## Разрывы и риски поддержки

1. Три поверхности не используют общий component/token package; nested demo — копия, возможен drift.
2. CSS каскад console style/imports/mapping/design-system и duplicated login selectors усложняет prediction и regression review.
3. Branding/logo variations и literal colors расходятся с единой library.
4. Landing обещает orders/analytics/prices/returns, real console показывает narrower capabilities; дизайн должен сообщать реализацию, не только визуальное намерение.
5. Demo изменяет локальное состояние и выглядит operational; обязательны demo labels и понятное различие с production service.
6. Accessibility и performance claims не подтверждены runtime measurement. WebGL/dpr/lazy-load решения есть, но FPS/CLS/LCP не измерялись.

## Рекомендации следующему этапу

Сначала согласовать capability copy и design contract. Затем централизовать semantic tokens и primitives с visual tests на ключевые состояния (empty/loading/error/unknown/stale/success/mobile). Не переносить demo order/pricing UI в production до появления реальных domain/API цепочек. В данном аудите redesign и изменения CSS не выполнялись.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [web/src/design-system.css:1](../../web/src/design-system.css#L1) | configuration / declarations; see file |
| [web/src/style.css:1](../../web/src/style.css#L1) | configuration / declarations; see file |
| [web/src/main.tsx:1](../../web/src/main.tsx#L1) | configuration / declarations; see file |
| [web/src/UI.tsx:1](../../web/src/UI.tsx#L1) | 51: Icon; 68: Brand; 76: PageHeader; 95: StatePanel; 116: ProviderBadge |
| [web/src/AppShell.tsx:1](../../web/src/AppShell.tsx#L1) | 14: Navigation; 31: AppShell |
| [web/src/IntegrationDialog.tsx:1](../../web/src/IntegrationDialog.tsx#L1) | 6: focusableElements; 33: IntegrationDialog |
| [web/src/WorkspaceSwitcher.tsx:1](../../web/src/WorkspaceSwitcher.tsx#L1) | 14: WorkspaceSwitcher |
| [E:/work/styk-demo/src/Hero3D.tsx:1](E:/work/styk-demo/src/Hero3D.tsx#L1) | 5: Objects |
| [E:/work/styk-demo/src/style.css:1](E:/work/styk-demo/src/style.css#L1) | configuration / declarations; see file |
| [E:/work/styk-landing/src/ConnectionScene.tsx:1](E:/work/styk-landing/src/ConnectionScene.tsx#L1) | 18: Paths; 27: ConnectionScene |
| [E:/work/styk-landing/src/style.css:1](E:/work/styk-landing/src/style.css#L1) | configuration / declarations; see file |
