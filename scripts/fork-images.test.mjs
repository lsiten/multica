import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";

const workflow = readFileSync(".github/workflows/fork-images.yml", "utf8");
assert.match(workflow, /push:\n    tags:\n      - "v\*\.\*\.\*"\n      - "!v\*-dirty\*"/);
assert.doesNotMatch(workflow, /branches:|workflow_dispatch:/);
assert.match(workflow, /build:\n    needs: verify/);
assert.match(workflow, /publish:\n    needs: \[verify, build\]/);
const validation = workflow.match(/id: version[\s\S]*?run: \|\n([\s\S]*?)\n\n/)[1]
  .replace(/^          /gm, "");
for (const [tag, expected] of [["v1.2.3", 0], ["v1.2.3-rc.1", 0], ["main", 1], ["v1.2", 1], ["v1.2.3-dirty", 1], ["v1.2.3;echo unsafe", 1]]) {
  const result = spawnSync("bash", ["-c", validation], {
    env: { ...process.env, RELEASE_TAG: tag, GITHUB_OUTPUT: "/dev/null" },
    encoding: "utf8",
  });
  assert.equal(result.status, expected, `${tag}: ${result.stderr}`);
}
const publish = workflow.slice(workflow.indexOf("  publish:"));
assert.ok(publish.includes(":${RELEASE_TAG}"));
assert.ok(publish.includes('if [[ "$RELEASE_TAG" != *-* ]]'));
assert.ok(publish.includes(":latest"));
assert.ok(!workflow.includes("release-desktop"));
const release = readFileSync(".github/workflows/release.yml", "utf8");
assert.match(release, /verify:\n(?:    #[^\n]*\n)*    if: github.repository != 'lsiten\/multica'/);
console.log("PASS: tag-only image build, version validation, stable promotion and no duplicate release");
