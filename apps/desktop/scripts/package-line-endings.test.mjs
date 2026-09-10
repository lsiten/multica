// @vitest-environment node

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { expect, it } from "vitest";
import { createViteServer } from "vitest/node";

it("imports the packaging CLI after a Windows-style Git checkout", async () => {
  const scriptPath = "apps/desktop/scripts/package.mjs";
  const repositoryRoot = [process.cwd(), resolve(process.cwd(), "../..")].find(
    (candidate) => existsSync(resolve(candidate, scriptPath)),
  );
  expect(repositoryRoot, "repository root not found").toBeTruthy();
  const checkoutRoot = mkdtempSync(join(tmpdir(), "multica-package-eol-"));
  const checkedOutScript = join(checkoutRoot, scriptPath);
  let server;

  try {
    const git = (...args) => execFileSync("git", args, {
      cwd: checkoutRoot,
      stdio: "pipe",
    });
    git("init", "-q");
    git("config", "core.autocrlf", "true");
    writeFileSync(
      join(checkoutRoot, ".gitattributes"),
      readFileSync(join(repositoryRoot, ".gitattributes")),
    );
    mkdirSync(dirname(checkedOutScript), { recursive: true });
    writeFileSync(
      checkedOutScript,
      readFileSync(join(repositoryRoot, scriptPath), "utf8").replace(/\r\n/g, "\n"),
    );
    git("add", ".gitattributes", scriptPath);
    rmSync(checkedOutScript);

    git("checkout", "--", scriptPath);
    server = await createViteServer({
      root: checkoutRoot,
      configFile: false,
      server: { middlewareMode: true, watch: null },
      logLevel: "silent",
    });
    const packaging = await server.ssrLoadModule(`/${scriptPath}`);

    expect(packaging.normalizeGitVersion("v1.4.2")).toBe("1.4.2");
  } finally {
    await server?.close();
    rmSync(checkoutRoot, { recursive: true, force: true });
  }
});
