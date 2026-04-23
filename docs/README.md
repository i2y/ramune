# Ramune landing page

Static site served from GitHub Pages at `https://i2y.github.io/ramune/`.

Source lives in `src/`, prerendered via `react-dom/server` and bundled for the
client via esbuild. Generated artifacts (`index.html`, `bundle.js`,
`llms.txt`, `llms-full.txt`) are committed so GitHub Pages can serve the repo
contents directly — no CI step required.

## Build

```sh
npm install
npm run build
```

Runs three steps:

1. `prerender` — React SSG via `react-dom/server.renderToStaticMarkup`,
   injects the static HTML into `src/index.template.html` at the
   `<!--SSG_CONTENT-->` marker, writes `index.html`.
2. `bundle` — esbuild IIFE → `bundle.js` (client hydration bundle).
3. `sync-llms` — copies root `../llms.txt` / `../llms-full.txt` into `docs/`
   so GitHub Pages serves them at `/ramune/llms.txt` etc.

Commit `index.html`, `bundle.js`, `llms.txt`, `llms-full.txt` along with any
source changes.

## Local preview

```sh
npm run serve
# http://localhost:8000
```

## Edit

- Copy lives in `src/variant-b.jsx`. EN/JA strings are paired via `tr('en','ja')`.
- Design tokens (`RAMUNE_BLUE`, `RAMUNE_AQUA`, `RAMUNE_INK`) are in `src/shared.jsx`.
- `src/app.jsx` mounts `<LangProvider><VariantB /></LangProvider>` via `hydrateRoot`.
- `src/prerender.jsx` is the Node-side entry used by the SSG pass.
- `src/index.template.html` is the HTML shell; `<!--SSG_CONTENT-->` gets
  replaced by the prerendered body. `index.html` is a pure build artifact —
  edit the template, not the output.

## Watch mode

```sh
npm run dev
```

Rebuilds `bundle.js` with sourcemaps on every source change. Does **not**
re-run `prerender`, so `index.html` stays pinned to the last `npm run build`
output while you iterate on the client bundle.

For a full end-to-end hydration check run `npm run build && npm run serve`,
open the site in DevTools, and watch for `Hydration failed` / `did not match`
warnings.

## Deploy

GitHub Pages is configured to serve `main:/docs`. Once a change is merged to
`main`, Pages picks it up automatically within a minute or two.

## AI crawlers / WebFetch

- `index.html` embeds the full landing prose post-prerender, so tools without
  JS execution (Claude WebFetch, LLM crawlers, SEO bots) see the complete
  content instead of an empty `<div id="root">`.
- `robots.txt` points crawlers at `sitemap.xml`.
- `llms.txt` / `llms-full.txt` mirror the repo-root copies (per
  [llmstxt.org](https://llmstxt.org/) convention).
