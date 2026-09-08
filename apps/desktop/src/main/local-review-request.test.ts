// @vitest-environment node
import { createServer } from "node:http";
import { expect, it, vi } from "vitest";
import { requestLocalReview } from "./local-review-request";

const input = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main", action: "approve", snapshot_id: "snapshot", command_id: "operation" };
const snapshot = { id: "snapshot", path: "/repo", branch: "feature", target: "main", head: "head", target_head: "base", base: "base", dirty: false, branches: [], commits: "", files: [], review: { snapshot_id: "snapshot", state: "approved", comment: "", merged_commit: "" } };

it("pins one profile across health and the authenticated local HTTP request", async () => {
  const received: string[] = [];
  const server = createServer((request, response) => {
    response.setHeader("Content-Type", "application/json");
    if (request.url === "/health") {
      response.end(JSON.stringify({ profile: "one", workspaces: [{ id: "ws", runtimes: ["runtime"] }] }));
      return;
    }
    if (request.headers.authorization !== "Bearer fixture-only" || request.headers["x-multica-profile"] !== "one") {
      response.writeHead(401).end();
      return;
    }
    request.setEncoding("utf8");
    let body = "";
    request.on("data", (chunk: string) => { body += chunk; });
    request.on("end", () => { received.push(body); response.end(JSON.stringify(snapshot)); });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture listener unavailable");
    const profile = { name: "one", port: address.port };
    const resolveProfile = vi.fn().mockResolvedValueOnce(profile).mockResolvedValue({ ...profile, name: "two" });
    const result = await requestLocalReview(input, {
      resolveProfile,
      health: async (selected) => (await fetch(`http://127.0.0.1:${selected.port}/health`, { signal: AbortSignal.timeout(3000) })).json(),
      review: async (selected, request) => {
        const response = await fetch(`http://127.0.0.1:${selected.port}/worktrees/review`, {
          method: "POST", signal: AbortSignal.timeout(3000), body: JSON.stringify(request),
          headers: { Authorization: "Bearer fixture-only", "X-Multica-Profile": selected.name },
        });
        if (!response.ok) throw new Error("wrong profile used for local request");
        return response.json();
      },
    });
    expect(resolveProfile).toHaveBeenCalledTimes(1);
    expect(result?.review.state).toBe("approved");
    expect(received).toHaveLength(1);
    expect(JSON.parse(received[0] ?? "null")).toMatchObject(input);
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});

it("does not send local operations for another runtime", async () => {
  const review = vi.fn();
  await expect(requestLocalReview(input, {
    resolveProfile: async () => ({ name: "one", port: 1234 }),
    health: async () => ({ profile: "one", workspaces: [{ id: "ws", runtimes: ["other-runtime"] }] }),
    review,
  })).resolves.toBeNull();
  expect(review).not.toHaveBeenCalled();
});

it("discovers a legacy task runtime once and preserves it for later local requests", async () => {
  const discoverRuntime = vi.fn().mockResolvedValue({ task_id: "task", workspace_id: "ws", runtime_id: "runtime" });
  const review = vi.fn().mockResolvedValue(snapshot);
  const transport = {
    resolveProfile: async () => ({ name: "one", port: 1234 }),
    health: async () => ({ profile: "one", workspaces: [{ id: "ws", runtimes: ["runtime"] }] }),
    discoverRuntime, review,
  };
  const result = await requestLocalReview({ ...input, runtime_id: undefined }, transport);
  expect(result?.runtime_id).toBe("runtime");
  expect(review).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ runtime_id: "runtime" }));
  await requestLocalReview({ ...input, runtime_id: result?.runtime_id }, transport);
  expect(discoverRuntime).toHaveBeenCalledTimes(1);
});

it("rejects discovery that returns another workspace before local access", async () => {
  const review = vi.fn();
  await expect(requestLocalReview({ ...input, runtime_id: undefined }, {
    resolveProfile: async () => ({ name: "one", port: 1234 }),
    health: async () => ({}), review,
    discoverRuntime: async () => ({ task_id: "task", workspace_id: "other", runtime_id: "runtime" }),
  })).rejects.toThrow("another task");
  expect(review).not.toHaveBeenCalled();
});
