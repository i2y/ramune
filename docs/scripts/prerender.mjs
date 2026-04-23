import { build } from "esbuild";
import { readFileSync, writeFileSync, unlinkSync, existsSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const here = dirname(fileURLToPath(import.meta.url));
const docsRoot = join(here, "..");
const tmp = join(docsRoot, ".prerender.cjs");
const templatePath = join(docsRoot, "src/index.template.html");
const outPath = join(docsRoot, "index.html");

await build({
  entryPoints: [join(docsRoot, "src/prerender.jsx")],
  bundle: true,
  platform: "node",
  format: "cjs",
  loader: { ".jsx": "jsx" },
  outfile: tmp,
  logLevel: "error",
  define: { "process.env.NODE_ENV": '"production"' },
});

const require = createRequire(import.meta.url);
const mod = require(tmp);
const body = mod.render();

const tpl = readFileSync(templatePath, "utf8");
const marker = "<!--SSG_CONTENT-->";
if (!tpl.includes(marker)) {
  console.error(`prerender: ${marker} not found in ${templatePath}`);
  process.exit(1);
}
writeFileSync(outPath, tpl.replace(marker, body));

if (existsSync(tmp)) unlinkSync(tmp);
console.log(`prerender: wrote ${body.length} bytes of SSG content to ${outPath}`);
