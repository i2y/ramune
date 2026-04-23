import { copyFileSync, existsSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, "../..");
const docsRoot = join(here, "..");

for (const name of ["llms.txt", "llms-full.txt"]) {
  const src = join(repoRoot, name);
  const dst = join(docsRoot, name);
  if (!existsSync(src)) {
    console.error(`sync-llms: missing ${src}`);
    process.exit(1);
  }
  copyFileSync(src, dst);
  console.log(`sync-llms: ${name}`);
}
