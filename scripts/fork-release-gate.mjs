#!/usr/bin/env node
import { appendFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { checkGate, decideScopes } from "./ci-scope.mjs";

export function verifyReleaseAcceptance({ expectedSha, checkoutSha, needs }) {
  if (!/^[0-9a-f]{40}$/.test(expectedSha ?? "") || checkoutSha !== expectedSha) {
    throw new Error("Release checkout does not match the requested commit SHA");
  }
  const fullScopes = decideScopes("workflow_dispatch", {});
  for (const [scope, selected] of Object.entries(fullScopes)) {
    // The raw quality scope is folded into frontend-build or quality_only.
    if (scope === "quality") continue;
    if (needs.changes?.outputs?.[scope] !== selected) {
      throw new Error(`Release acceptance requires full CI scope ${scope}=${selected}`);
    }
  }
  checkGate(needs, { frontend: "frontend", backend: "backend" });
  return checkoutSha;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const acceptedSha = verifyReleaseAcceptance({
      expectedSha: process.env.GITHUB_SHA,
      checkoutSha: execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim(),
      needs: JSON.parse(process.env.CI_NEEDS),
    });
    appendFileSync(process.env.GITHUB_OUTPUT, `accepted_sha=${acceptedSha}\n`);
    console.log(`Release acceptance passed for ${acceptedSha}`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
