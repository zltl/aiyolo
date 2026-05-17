---
name: frontend-design-guide
description: 'Design and implement frontend UI with responsive layout, typography, spacing, color tokens, accessibility, and performance guardrails. Use for web design, frontend design, landing pages, dashboards, admin consoles, forms, marketing pages, component styling, or when starting a new web UI and you want a concrete design direction instead of a generic layout.'
argument-hint: 'Describe the page, target users, brand or tone, constraints, and frontend stack.'
---

# Frontend Design Guide

Use this skill when you are starting a new web page, app surface, or component set and want a grounded design direction before writing code.

## When to Use

- Starting a new frontend surface from scratch
- Redesigning a page that looks generic or inconsistent
- Turning product requirements into layout, visual tokens, and implementation notes
- Auditing an existing UI for accessibility or performance regressions

## Procedure

1. Capture constraints first.
   - Product or page type
   - Primary user task
   - Brand tone or visual references
   - Target devices and breakpoints
   - Existing design system or component library
2. Build a brief with [the template](./assets/design-brief-template.md).
3. Use [the MDN foundations guide](./references/mdn-frontend-foundations.md) to choose:
   - semantic structure
   - layout method: normal flow, flexbox, grid, positioning
   - type scale, spacing scale, and color tokens
   - responsive rules, image handling, and motion limits
   - accessibility and performance guardrails
4. Produce implementation output in this order:
   - page or component hierarchy
   - design tokens
   - layout plan for mobile, tablet, and desktop
   - interaction states and motion notes
   - accessibility checklist
   - performance checklist
5. If coding in an existing app, preserve the current design system unless the user asks for a new visual direction.

## Output Contract

When this skill is used, prefer delivering:

- a short design brief
- a small token set for color, type, spacing, radius, and shadow
- a layout description for major breakpoints
- implementation notes tied to the actual stack
- a short verification checklist for accessibility and performance

## Guardrails

- Start mobile-first and expand upward.
- Prefer semantic HTML over div-heavy structures.
- Treat accessibility and performance as design requirements, not polish.
- Avoid default, interchangeable UI patterns when the user asks for design work.
- When no design system exists, choose an intentional type direction instead of default system typography.
- Reuse existing components and tokens when the codebase already has them.

## References

- [MDN frontend foundations](./references/mdn-frontend-foundations.md)
- [Design brief template](./assets/design-brief-template.md)