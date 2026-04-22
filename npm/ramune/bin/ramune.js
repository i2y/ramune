#!/usr/bin/env node
"use strict";

const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");

const PLATFORM_PACKAGES = {
  "darwin-arm64": "@ramune/darwin-arm64",
  "linux-x64": "@ramune/linux-x64",
  "linux-arm64": "@ramune/linux-arm64",
  "win32-x64": "@ramune/win32-x64",
  "win32-arm64": "@ramune/win32-arm64",
};

const GO_INSTALL_URL =
  "https://github.com/i2y/ramune#install";

function fail(msg) {
  process.stderr.write(`ramune: ${msg}\n`);
  process.exit(1);
}

function resolveBinary() {
  const key = `${process.platform}-${process.arch}`;
  const pkg = PLATFORM_PACKAGES[key];
  if (!pkg) {
    fail(
      `unsupported platform ${key}.\n` +
        `supported: ${Object.keys(PLATFORM_PACKAGES).join(", ")}\n` +
        `fallback: build from source — see ${GO_INSTALL_URL}`
    );
  }

  let manifestPath;
  try {
    manifestPath = require.resolve(`${pkg}/package.json`);
  } catch (_) {
    fail(
      `missing platform package ${pkg}.\n` +
        `this usually means your package manager skipped optionalDependencies.\n` +
        `try reinstalling with --force, or see ${GO_INSTALL_URL}`
    );
  }

  const binName = process.platform === "win32" ? "ramune.exe" : "ramune";
  const binPath = path.join(path.dirname(manifestPath), "bin", binName);
  if (!fs.existsSync(binPath)) {
    fail(`binary not found at ${binPath}`);
  }
  return binPath;
}

function run() {
  const binary = resolveBinary();
  const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });

  const signals =
    process.platform === "win32"
      ? ["SIGINT", "SIGTERM", "SIGBREAK", "SIGHUP"]
      : ["SIGINT", "SIGTERM", "SIGHUP"];
  for (const sig of signals) {
    try {
      process.on(sig, () => {
        try {
          child.kill(sig);
        } catch (_) {
          /* child already gone */
        }
      });
    } catch (_) {
      /* signal not supported on this platform */
    }
  }

  child.on("error", (err) => {
    fail(`failed to launch ${binary}: ${err.message}`);
  });

  child.on("close", (code, signal) => {
    if (signal) {
      if (process.platform !== "win32") {
        try {
          process.kill(process.pid, signal);
          return;
        } catch (_) {
          /* fall through */
        }
      }
      process.exit(1);
    } else {
      process.exit(code ?? 0);
    }
  });
}

run();
