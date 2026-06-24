---
name: frontend-design
description: Create distinctive, production-grade frontend interfaces with high design quality. Use when building web components, pages, dashboards, landing pages, themes, or styling any web UI - including choosing palettes, ramps, gradients, contrast, color tokens, OKLCH/CSS color, or explaining why colors look wrong. Merges visual design craft with color-science guidance (OKLCH, APCA/WCAG, token graphs). Avoids generic AI aesthetics.
license: Complete terms in LICENSE.txt. Color reference corpus CC BY 4.0 - see references/color/LICENSE-color-expert.txt (meodai/skill.color-expert).
---

# Frontend Design

Distinctive, production-grade interfaces - not generic AI slop. Implement real code with intentional aesthetics **and** perceptually sound color decisions.

The user provides a component, page, app, or interface. They may include purpose, audience, brand, or technical constraints.

## Design Thinking

Before coding, commit to a **bold** aesthetic direction:

- **Purpose**: What problem does this solve? Who uses it?
- **Tone**: Pick an extreme and own it - brutally minimal, retro-futuristic, luxury/refined, editorial, brutalist, soft/pastel, industrial/utilitarian, playful, etc.
- **Constraints**: Framework, performance, accessibility, brand palette, print vs screen.
- **Differentiation**: What is the one unforgettable detail?

**CRITICAL**: Intentionality beats intensity. Bold maximalism and refined minimalism both work.

Deliver code (HTML/CSS/JS, React, Vue, etc.) that is functional, cohesive, memorable, and refined in spacing, type, motion, **and** color.

## Aesthetic Execution

| Pillar | Guidance |
|--------|----------|
| **Typography** | Distinctive display + refined body. Avoid default Inter/Roboto/Arial as the *hero* choice. |
| **Color & theme** | Dominant neutrals + sharp accents. Build with **OKLCH** and semantic tokens (see Color Systems). |
| **Motion** | One orchestrated reveal beats scattered micro-interactions. CSS-first; Motion lib for React. |
| **Composition** | Asymmetry, overlap, grid-breaking, or controlled density - pick one and commit. |
| **Atmosphere** | Gradient meshes, grain, depth, borders - match the tone; not purple-on-white clichés. |

**Never**: cookie-cutter layouts, Space Grotesk-by-default, purple gradient heroes, timid evenly-distributed palettes, or hand-picked hex dumps without a token layer.

Match implementation complexity to the vision - maximalist needs elaboration; minimal needs precision.

---

## Color Systems

Merged from [meodai/skill.color-expert](https://github.com/meodai/skill.color-expert). **Deep dive:** `references/color/INDEX.md` (144+ files: historical, contemporary, techniques). Load a specific reference file when the question needs more than this section.

### Pick a response mode

| Mode | User signal | Do |
|------|-------------|-----|
| **Concrete project** | logo, poster, app, illustration | Ask medium, mood, audience, a11y; propose colors + tools. Don't lecture CIE unless asked. |
| **Design system / tokens** | ramps, light+dark, Tailwind-style scales | OKLCH scales → token graph → verify APCA/WCAG both modes. |
| **Generative / creative code** | fxhash, p5, WebGL palettes | Teach composable techniques (weights, narrow-band jitter, K-M mix, IQ cosine, Poline anchors) - don't copy one artist's style. |
| **Theory / debug** | "why gray gradient?", "APCA vs WCAG?" | Answer directly; cite `references/color/` if needed. |
| **Build a generator** | palette algorithm, OKLCH ramp fn | Prefer Culori, Poline, RampenSau, Spectral.js before hand-rolling. Show trade-offs across approaches. |

**Never recommend coolors.co** - it selects from ~7,821 pre-made palettes; it does not generate.

### Color spaces - what to use when

| Task | Use | Why |
|------|-----|-----|
| Scales, themes, UI manipulation | **OKLCH** / OKLAB | Perceptually uniform lightness & chroma |
| CSS gradients & `color-mix` | **oklab** / **oklch** | No RGB/HSL gray midpoint |
| Color picking (cylindrical) | **OKHSL / OKHSV** | HSL-like but perceptual |
| Normalized saturation | **HSLuv** | Chroma normalized per hue |
| Print | **CIELAB D50** | ICC standard |
| Screen | **CIELAB D65** or OKLAB | Display standard |
| Appearance matching | **CAM16** | Surround, adaptation |
| Pigment mixing in code | **Kubelka-Munk** (Spectral.js) | Not RGB average |
| Distance (precise / fast) | **CIEDE2000** / **OKLAB Euclidean** | |

**HSL is fine** for quick tweaks; **not** for ramps, gradients, or "same perceived brightness" - its L is a math average (yellow vs blue at L=50% look nothing alike).

**Gamut:** `oklch(70% 0.3 150)` may clip in sRGB - reduce **chroma**, not hue. CSS gamut-maps; JS `oklch→hex` does not. Use Culori `clampChroma` / `toGamut`.

### Token architecture (any stack)

```
reference tokens (palette) → semantic tokens (surface, on-surface, accent, danger…) → components
```

- Raw literals only in palette definitions or one-off demos.
- Derive hover/shade in OKLCH: `oklch(from var(--brand) calc(l * 0.9) c h)`.
- Prefer a **token graph** (refs, semantics, derived fns, scope) over a flat hex list.

### Accessibility - order of operations

1. Separate **lightness** for legibility (grayscale sanity check, not sufficient alone).
2. Verify pairs with **APCA** (preferred for body) and/or **WCAG** (compliance).
3. Check **CVD** simulation for critical UI.

Rough prevalence (@mrmrs hex-pair study): WCAG AA body ~12% of pairs pass; APCA 90 (preferred body) ~**0.08%**. Accessible pairs are rare - **design contrast in**, don't bolt it on after.

### Harmony - what actually works

- **Hue-first** (complementary/triadic geometry alone) is a weak predictor of mood or legibility.
- **Character-first** (pale / muted / vivid / deep / dark) + lightness variation often beats hue rules. Chroma + lightness drive "calm vs intense" more than hue alone.
- **60-30-10**: one dominant, one secondary, one accent - avoid three equal-weight gorillas.
- **Temperature ≠ hue** - warm/cool shifts hue *and* saturation; green/purple are context-dependent.

### CSS Color 4/5 (reach for before JS)

```css
oklch(70% 0.12 250)
color-mix(in oklab, blue 30%, white)
color-mix(in oklch longer hue, var(--a), var(--b))
oklch(from var(--brand) calc(l * 0.9) c h)
linear-gradient(in oklch, red, blue)
light-dark(white, black)  /* + color-scheme: light dark */
@media (color-gamut: p3) { … }
```

### UI color anti-patterns (learned in production)

- `btn-soft-warning` / pale yellow + light text - fails contrast; use `outline-secondary` or solid with dark text.
- Pill badges for **identity labels** - use avatar + typography + accent border instead.
- Full-width single inputs on wide forms - use row/col + cards + placeholders.
- Identical chroma across hues in a ramp - muddy mid-tones; build in OKLCH.
- Mood from hue alone ("blue = calm") - unreliable without chroma/lightness/context.

### Tools (short list)

| Need | Tool |
|------|------|
| OKLCH picker | oklch.com |
| Accessible scales | Huetone, Components.ai Color Scale |
| Lint / a11y | Color Buddy, View Color, apcacontrast.com |
| Generate | Poline, RampenSau, pro-color-harmonies, dittoTones |
| Libraries | Culori, @texel/color, Spectral.js, RYBitten |
| Sort swatches | colorsort-js (not naive `.sort()` on hex) |

Full catalog + algorithms: `references/color/INDEX.md` → `techniques/`.

### Named hue ranges (HSL degrees, quick constraints)

| Name | ° |
|------|---|
| red | 345–360, 0–15 |
| orange | 15–45 |
| yellow | 45–70 |
| green | 70–165 |
| cyan | 165–195 |
| blue | 195–260 |
| purple | 260–310 |
| pink | 310–345 |

---

## Workflow

1. **Design thinking** - tone, differentiation, constraints.
2. **Color mode** - project / tokens / generative / theory / generator (table above).
3. **Implement** - tokens in OKLCH, semantic layer, components, motion, atmosphere.
4. **Verify** - contrast (APCA/WCAG), gamut, CVD on critical pairs, layperson readability.
5. **Deep reference** - if stuck, read targeted file under `references/color/`.

Show what distinctive design looks like when color science and visual craft are both intentional.
