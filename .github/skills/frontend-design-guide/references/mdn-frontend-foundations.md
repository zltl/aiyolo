# MDN Frontend Foundations

This reference condenses official MDN learning material into a practical guide for designing and shipping frontend UI.

## Official Sources

- MDN CSS styling basics: https://developer.mozilla.org/en-US/docs/Learn_web_development/Core/Styling_basics
- MDN CSS layout: https://developer.mozilla.org/en-US/docs/Learn_web_development/Core/CSS_layout
- MDN Accessibility: https://developer.mozilla.org/en-US/docs/Learn_web_development/Core/Accessibility
- MDN Web performance: https://developer.mozilla.org/en-US/docs/Learn_web_development/Extensions/Performance
- MDN Layout cookbook: https://developer.mozilla.org/en-US/docs/Web/CSS/How_to/Layout_cookbook

## 1. Start With Structure

- Use semantic HTML for page regions, headings, navigation, forms, and lists.
- Let content hierarchy drive the layout. Decide what users must see first on a narrow screen.
- Choose the simplest layout primitive that matches the problem.
  - Normal flow for documents and article-like screens
  - Flexbox for one-dimensional alignment and distribution
  - Grid for two-dimensional page sections
  - Positioning only when overlap or fixed placement is truly required

## 2. Build a Small Visual System

- Define a small token set before styling screens.
  - 3 to 5 type sizes
  - 1 spacing scale
  - 1 radius scale
  - 1 shadow scale
  - semantic color tokens such as background, surface, text, muted, accent, border, success, warning, danger
- Use relative units for fluid behavior where practical.
- Prefer strong hierarchy through size, spacing, weight, and contrast before adding decoration.
- Use backgrounds, borders, and restrained effects intentionally. Avoid piling on gradients, shadows, and animations without a content purpose.

## 3. Design Responsive Layouts

- Start mobile-first.
- Use content-driven breakpoints instead of device-name breakpoints.
- Make widths fluid first; add media queries only when the layout starts breaking.
- For responsive content:
  - collapse multi-column layouts into a readable single-column flow
  - keep tap targets comfortably sized
  - avoid forcing critical content below oversized hero sections
  - test navigation, tables, and forms on narrow widths early

## 4. Accessibility Is Part of Design

- Keep heading order logical and labels explicit.
- Ensure interactive elements are keyboard reachable with visible focus styles.
- Do not rely on color alone to communicate status or errors.
- Keep text contrast strong enough for the background behind it.
- Provide text alternatives for informative images and accessible names for controls.
- Respect reduced-motion preferences for nonessential animation.
- For forms, design validation, helper text, and error states together instead of treating them as edge cases.

## 5. Performance Shapes Perceived Quality

- Optimize the biggest payloads first, especially images, video, and third-party scripts.
- Use responsive images and avoid serving oversized assets.
- Keep DOM size and CSS complexity reasonable.
- Defer or split noncritical JavaScript so the first render happens quickly.
- Be cautious with effects that are expensive to paint or animate.
- Define a performance budget early enough that design choices can respect it.

## 6. A Practical Delivery Sequence

1. Write a brief: users, task, tone, constraints.
2. Sketch information hierarchy and mobile flow.
3. Choose tokens for color, type, spacing, radius, and shadow.
4. Map sections to layout primitives.
5. Define states: hover, focus, active, disabled, loading, empty, error.
6. Check keyboard flow, contrast, and text alternatives.
7. Review asset weight and script cost before polishing.

## 7. Default Questions To Ask Before Coding

- What is the primary user action on this screen?
- Which content is essential above the first scroll?
- Does this layout still work when text becomes longer?
- Can the page be used with keyboard only?
- What happens on small screens and slower networks?
- Which design elements are carrying meaning, and which are only decoration?