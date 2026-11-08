import assert from "node:assert/strict";
import test from "node:test";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { decideScopes } from "./ci-scope.mjs";
import { verifyReleaseAcceptance } from "./fork-release-gate.mjs";

const tagSha = "1234567890abcdef1234567890abcdef12345678";
function successfulAcceptance() {
  return {
    expectedSha: tagSha,
    checkoutSha: tagSha,
    needs: {
      changes: { result: "success", outputs: decideScopes("workflow_dispatch", {}) },
      frontend: { result: "success" },
      backend: { result: "success" },
    },
  };
}

test("accepts the exact tag commit after complete full-scope validation", () => {
  assert.equal(verifyReleaseAcceptance(successfulAcceptance()), tagSha);
});

test("rejects validation performed on another commit", () => {
  const input = successfulAcceptance();
  input.checkoutSha = "abcdef1234567890abcdef1234567890abcdef12";
  assert.throws(() => verifyReleaseAcceptance(input));
});

test("rejects path-filtered green CI even if its selected jobs succeeded", () => {
  const input = successfulAcceptance();
  input.needs.changes.outputs.full = "false";
  input.needs.changes.outputs.sqlc = "false";
  assert.throws(() => verifyReleaseAcceptance(input));
});

for (const result of ["failure", "cancelled", "skipped", undefined]) {
  test(`rejects incomplete backend acceptance ${result}`, () => {
    const input = successfulAcceptance();
    input.needs.backend.result = result;
    assert.throws(() => verifyReleaseAcceptance(input));
  });
}

test("rejects missing frontend acceptance", () => {
  const input = successfulAcceptance();
  delete input.needs.frontend;
  assert.throws(() => verifyReleaseAcceptance(input));
});


test("the workflow CLI emits an immutable SHA only for a complete matching checkout", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "multica-release-acceptance-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const root = fileURLToPath(new URL("../", import.meta.url));
  const checkoutSha = execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
  for (const scenario of ["matching", "wrong-sha", "skipped-backend"]) {
    const output = join(directory, `${scenario}.out`);
    const input = successfulAcceptance();
    if (scenario === "skipped-backend") input.needs.backend.result = "skipped";
    const result = spawnSync(process.execPath, [fileURLToPath(new URL("./fork-release-gate.mjs", import.meta.url))], {
      cwd: root,
      encoding: "utf8",
      env: {
        ...process.env,
        GITHUB_SHA: scenario === "wrong-sha" ? "0".repeat(40) : checkoutSha,
        CI_NEEDS: JSON.stringify(input.needs),
        GITHUB_OUTPUT: output,
      },
    });
    if (scenario === "matching") {
      assert.equal(result.status, 0, result.stderr);
      assert.equal(readFileSync(output, "utf8"), `accepted_sha=${checkoutSha}\n`);
    } else {
      assert.notEqual(result.status, 0);
      assert.equal(existsSync(output), false, "Rejected acceptance must not produce a checkout SHA");
    }
  }
});

test("rejects a claimed full run that skipped a required SQL scope", () => {
  const input = successfulAcceptance();
  input.needs.changes.outputs.sqlc = "false";
  assert.throws(() => verifyReleaseAcceptance(input), /scope sqlc=true/);
});

test("rejects an invalid commit identity even when both values match", () => {
  const input = successfulAcceptance();
  input.expectedSha = "not-a-commit";
  input.checkoutSha = input.expectedSha;
  assert.throws(() => verifyReleaseAcceptance(input));
});
