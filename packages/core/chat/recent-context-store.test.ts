import { beforeEach, describe, expect, it } from "vitest";
import { selectRecentContexts, useRecentContextStore } from "./recent-context-store";

beforeEach(() => {
  useRecentContextStore.setState({ byWorkspace: {} });
});

describe("useRecentContextStore.recordVisit", () => {
  it("keeps visits namespaced by workspace id", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-b", { type: "project", id: "project-1" });

    const state = useRecentContextStore.getState().byWorkspace;
    expect(state["ws-a"]?.map((e) => `${e.type}:${e.id}`)).toEqual(["issue:issue-1"]);
    expect(state["ws-b"]?.map((e) => `${e.type}:${e.id}`)).toEqual(["project:project-1"]);
  });

  it("moves the most recent visit to the front and dedupes by type and id", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "same-id" });
    recordVisit("ws-a", { type: "project", id: "same-id" });
    recordVisit("ws-a", { type: "issue", id: "same-id" });

    const keys = useRecentContextStore
      .getState()
      .byWorkspace["ws-a"]?.map((e) => `${e.type}:${e.id}`);
    expect(keys).toEqual(["issue:same-id", "project:same-id"]);
  });

  it("caps each workspace bucket at 20 entries", () => {
    const { recordVisit } = useRecentContextStore.getState();
    for (let i = 0; i < 25; i++) recordVisit("ws-a", { type: "issue", id: `issue-${i}` });
    expect(useRecentContextStore.getState().byWorkspace["ws-a"]).toHaveLength(20);
  });

  it("stores a local display snapshot for recent entries", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", {
      type: "issue",
      id: "issue-1",
      label: "MUL-1",
      subtitle: "Fix login redirect",
      status: "todo",
      projectStatus: "in_progress",
      icon: "🚀",
    });

    expect(useRecentContextStore.getState().byWorkspace["ws-a"]?.[0]).toMatchObject({
      type: "issue",
      id: "issue-1",
      label: "MUL-1",
      subtitle: "Fix login redirect",
      status: "todo",
      projectStatus: "in_progress",
      icon: "🚀",
    });
  });
});

describe("useRecentContextStore.forgetContext", () => {
  it("removes a single context from the workspace bucket", () => {
    const { recordVisit, forgetContext } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-a", { type: "project", id: "project-1" });
    recordVisit("ws-a", { type: "issue", id: "issue-2" });

    forgetContext("ws-a", { type: "project", id: "project-1" });

    const keys = useRecentContextStore
      .getState()
      .byWorkspace["ws-a"]?.map((e) => `${e.type}:${e.id}`);
    expect(keys).toEqual(["issue:issue-2", "issue:issue-1"]);
  });

  it("does not touch other workspaces' buckets", () => {
    const { recordVisit, forgetContext } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-b", { type: "issue", id: "issue-1" });

    forgetContext("ws-a", { type: "issue", id: "issue-1" });

    const state = useRecentContextStore.getState().byWorkspace;
    expect(state["ws-a"]).toBeUndefined();
    expect(state["ws-b"]?.map((e) => e.id)).toEqual(["issue-1"]);
  });
});

describe("useRecentContextStore attachment context", () => {
  it("records an attachment as a distinct context type alongside issue/project", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "issue-1" });
    recordVisit("ws-a", { type: "project", id: "project-1" });
    recordVisit("ws-a", { type: "attachment", id: "attach-1", label: "spec.md", subtitle: "Markdown" });

    const keys = useRecentContextStore
      .getState()
      .byWorkspace["ws-a"]?.map((e) => `${e.type}:${e.id}`);
    // most-recent-first; the attachment visit is newest
    expect(keys).toEqual(["attachment:attach-1", "project:project-1", "issue:issue-1"]);
  });

  it("keeps an attachment entry with the same id distinct from an issue of the same id", () => {
    const { recordVisit } = useRecentContextStore.getState();
    recordVisit("ws-a", { type: "issue", id: "same-id" });
    recordVisit("ws-a", { type: "attachment", id: "same-id", label: "same-id.md" });

    const keys = useRecentContextStore
      .getState()
      .byWorkspace["ws-a"]?.map((e) => `${e.type}:${e.id}`);
    expect(keys).toEqual(["attachment:same-id", "issue:same-id"]);
  });
});

describe("selectRecentContexts", () => {
  it("returns a stable empty array when wsId is null or unknown", () => {
    const a = selectRecentContexts(null)(useRecentContextStore.getState());
    const b = selectRecentContexts(null)(useRecentContextStore.getState());
    const c = selectRecentContexts("missing")(useRecentContextStore.getState());
    expect(a).toBe(b);
    expect(a).toBe(c);
    expect(a).toEqual([]);
  });
});
