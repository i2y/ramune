# Ramune landing page

Static site served from GitHub Pages at `https://i2y.github.io/ramune/`.

Source lives in `src/`, built into `bundle.js` via esbuild. The built bundle is
committed so GitHub Pages can serve the repo contents directly — no CI step
required.

## Build

```sh
npm install
npm run build
```

This produces `bundle.js` (React + app code, minified, IIFE). Commit the
result along with any source changes.

## Local preview

```sh
npm run serve
# http://localhost:8000
```

## Edit

- Copy lives in `src/variant-b.jsx`. EN/JA strings are paired via `tr('en','ja')`.
- Design tokens (`RAMUNE_BLUE`, `RAMUNE_AQUA`, `RAMUNE_INK`) are in `src/shared.jsx`.
- `src/app.jsx` just mounts `<LangProvider><VariantB /></LangProvider>`.

## Watch mode

```sh
npm run dev
```

Rebuilds `bundle.js` with sourcemaps on every change. Run `npm run serve` in
another terminal.

## Deploy

GitHub Pages is configured to serve `main:/docs`. Once a change is merged to
`main`, Pages picks it up automatically within a minute or two.
