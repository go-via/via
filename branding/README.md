# VIA — branding assets

Theme-aware logo marks for GitHub READMEs and app surfaces, exported from
[Claude Design](https://claude.ai/design). GitHub serves SVGs as proxied
images, so theme response uses the `<picture>` element with separate
light/dark files (a single SVG with an internal `prefers-color-scheme` style
does **not** survive GitHub's image sanitiser).

`-light` files use ink (`#0b0b0f`) — for light backgrounds.
`-dark` files use cream (`#efece4`) — for dark backgrounds.
Amber is always `#ffbf00`.

The snippets below use paths relative to the repository root, ready to paste
into the top-level `README.md`.

## Wordmark

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="branding/wordmark-amber-dark.svg">
  <img alt="via" src="branding/wordmark-amber-light.svg" width="180">
</picture>
```

## Bolt

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="branding/bolt-dark.svg">
  <img alt="VIA" src="branding/bolt-light.svg" width="96">
</picture>
```

## Animation (APNG)

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="branding/punch-dark.png">
  <img alt="VIA" src="branding/punch-light.png" width="220">
</picture>
```

## App icon

App-icon badges carry their own background, so they need no theme variant:

```html
<img alt="VIA" src="branding/icon-ink-amber.svg" width="96">
```

## Files

| Asset | Light | Dark |
|---|---|---|
| Wordmark | `wordmark-light.svg` | `wordmark-dark.svg` |
| Wordmark (amber slash) | `wordmark-amber-light.svg` | `wordmark-amber-dark.svg` |
| Bolt | `bolt-light.svg` | `bolt-dark.svg` |
| Bolt outline | `bolt-outline-light.svg` | `bolt-outline-dark.svg` |
| Bolt (amber) | `bolt-amber.svg` | — |
| Animation | `punch-light.png` | `punch-dark.png` |
| App icon · ink/amber | `icon-ink-amber.svg` | — |
| App icon · amber/ink | `icon-amber-ink.svg` | — |
| App icon · cream/ink | `icon-cream-ink.svg` | — |
| App icon · ink/amber outline | `icon-ink-amber-outline.svg` | — |
