// @vitest-environment node
import { expect, it } from "vitest";
import { chatFileDirectories } from "./file-directories";

it("keeps resource authorization selectable when a stale run names the same path", () => {
  const directories = chatFileDirectories({
    workspace_id: "ws", attachments: [], repos: [],
    directories: [{ project_resource_id: "resource", daemon_id: "daemon", runtime_id: "runtime", path: "/project" }],
    tasks: [{ id: "stale", runtime_id: "runtime", work_dir: "/project", created_at: "2026-01-01" }],
  });
  expect(directories).toHaveLength(2);
  expect(directories[0]).toMatchObject({ projectResourceId: "resource", path: "/project" });
  expect(directories[0]?.taskId).toBeUndefined();
});

it("merges session and project worktrees while excluding another project's run", () => {
  const current = { id: "current", project_id: "project", runtime_id: "runtime", work_dir: "/current", created_at: "2026-01-01" };
  const directories = chatFileDirectories({
    workspace_id: "ws", attachments: [], repos: [], tasks: [current],
    project_tasks: [current, { ...current, id: "historical", work_dir: "/historical" }, { ...current, id: "other", project_id: "other", work_dir: "/other" }],
  }, "project");
  expect(directories.map((directory) => directory.path)).toEqual(["/current", "/historical"]);
});
