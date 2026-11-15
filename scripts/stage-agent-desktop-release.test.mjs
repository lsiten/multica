import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { downloadVerified } from "./stage-agent-desktop-release.mjs";

const directories = [];

test.afterEach(async () => {
  await Promise.all(directories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function destination() {
  const directory = await mkdtemp(join(tmpdir(), "agent-desktop-release-test-"));
  directories.push(directory);
  return join(directory, "asset.tar.gz");
}

test("writes a downloaded release only after the pinned checksum matches", async () => {
  const bytes = Buffer.from("verified release archive");
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  const target = await destination();
  let calls = 0;
  const fetchImpl = async () => {
    calls += 1;
    return new Response(bytes, {
      status: 200,
      headers: { "content-length": String(bytes.length) },
    });
  };

  await downloadVerified(fetchImpl, "https://fixture.test/release", target, sha256, 1024);

  assert.deepEqual(await readFile(target), bytes);
  assert.equal(calls, 1);
});

test("rejects a release whose bytes do not match the pinned checksum", async () => {
  const target = await destination();
  const fetchImpl = async () => new Response("tampered", { status: 200 });

  await assert.rejects(downloadVerified(
    fetchImpl,
    "https://fixture.test/release",
    target,
    "0".repeat(64),
    1024,
  ), /SHA-256 mismatch/);
  await assert.rejects(readFile(target));
});
