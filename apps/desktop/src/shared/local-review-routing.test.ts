import { expect, it } from "vitest";
import { ownsReviewRuntime } from "./local-review-routing";

const health = { profile: "desktop", workspaces: [{ id: "workspace", runtimes: ["runtime"] }] };
const request = { task_id: "task", workspace_id: "workspace", runtime_id: "runtime", path: "/shared-looking/path", target: "main" };

it("routes directly only for the owning profile, workspace and runtime", () => {
  expect(ownsReviewRuntime(health, request, "desktop")).toBe(true);
  expect(ownsReviewRuntime(health, { ...request, runtime_id: "remote" }, "desktop")).toBe(false);
  expect(ownsReviewRuntime(health, { ...request, workspace_id: "other" }, "desktop")).toBe(false);
  expect(ownsReviewRuntime(health, request, "other-profile")).toBe(false);
  expect(ownsReviewRuntime(health, { ...request, runtime_id: undefined }, "desktop")).toBe(false);
  expect(ownsReviewRuntime({ workspaces: health.workspaces }, request, "desktop")).toBe(false);
});
