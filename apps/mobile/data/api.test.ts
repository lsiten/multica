// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "./api";
import { invalidateSessionEpoch } from "./session-epoch";

// api.ts refuses to load without a base URL; set it before the import runs.
vi.hoisted(() => {
  process.env.EXPO_PUBLIC_API_URL = "https://api.example.test";
});

describe("API session ownership", () => {
  afterEach(() => {
    api.setToken(null);
    api.setOptions({ onUnauthorized: undefined });
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it.each(["renewal", "account switch", "logout"] as const)("ignores an old 401 after %s", async (transition) => {
    let respond!: (response: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { respond = resolve; })));
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    const unauthorized = vi.fn();
    api.setOptions({ onUnauthorized: unauthorized });
    api.setToken("old-token");
    const pending = api.deleteComment("comment-1");
    const rejected = expect(pending).rejects.toMatchObject({ status: 401 });
    if (transition !== "renewal") invalidateSessionEpoch();
    if (transition !== "logout") api.setToken("new-token");
    respond(new Response("{}", { status: 401 }));
    await rejected;
    expect(unauthorized).not.toHaveBeenCalled();
    expect(api.getToken()).toBe(transition === "logout" ? "old-token" : "new-token");
  });

  it("ends the session when its current credential is rejected", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 401 })));
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    const unauthorized = vi.fn();
    api.setOptions({ onUnauthorized: unauthorized });
    api.setToken("current-token");
    await expect(api.deleteComment("comment-1")).rejects.toMatchObject({ status: 401 });
    expect(unauthorized).toHaveBeenCalledOnce();
  });
});

// The real store pulls in expo-secure-store; the client only needs the slug.
vi.mock("@/data/workspace-store", () => ({ getCurrentSlug: () => null }));

describe("native notification API boundaries", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("treats malformed configuration as unavailable and never accepts a malformed registration", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ enabled: "yes", topics: [42] })));
    await expect(api.getMobilePushConfiguration()).resolves.toEqual({ enabled: false, topics: [], environments: [] });
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ registered: "yes" })));
    await expect(api.registerMobilePushDevice({ token: "abcd", topic: "test.app", environment: "sandbox" })).rejects.toThrow("Push registration failed");
  });

  it("marks a push's source workspace read and rejects a malformed read receipt", async () => {
    const fetchMock = vi.fn(async () => Response.json({ id: "inbox-1", workspace_id: "workspace-2", type: "mentioned" }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(api.markInboxRead("inbox-1", "other-workspace")).resolves.toMatchObject({ workspace_id: "workspace-2", id: "inbox-1" });
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/api/inbox/inbox-1/read"), expect.objectContaining({ headers: expect.objectContaining({ "X-Workspace-Slug": "other-workspace" }) }));
    fetchMock.mockResolvedValueOnce(Response.json({ id: 42 }));
    await expect(api.markInboxRead("inbox-1", "other-workspace")).rejects.toThrow("Inbox read response invalid");
  });
});

describe("api.listAgents", () => {
  afterEach(() => vi.unstubAllGlobals());

  it.each([
    [undefined, "/api/agents"],
    [{ includeArchived: false }, "/api/agents"],
    [{ includeArchived: true }, "/api/agents?include_archived=true"],
  ])("with %j requests %s", async (opts, path) => {
    const fetchMock = vi.fn(async () => Response.json([]));
    vi.stubGlobal("fetch", fetchMock);
    await expect(api.listAgents(opts)).resolves.toEqual([]);
    expect(fetchMock).toHaveBeenCalledExactlyOnceWith(`https://api.example.test${path}`, expect.any(Object));
  });
});

// The server leaves guests out of the member list unless the caller declares
// it can render them; useIsGuest reads the caller's own row from that list.
describe("api.listMembers", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("advertises the guest capability", async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => Response.json([]));
    vi.stubGlobal("fetch", fetchMock);
    await api.listMembers("ws-1");
    const headers = fetchMock.mock.calls[0]?.[1]?.headers as Record<string, string>;
    expect(headers["X-Client-Capabilities"]).toBe("workspace-guests-v1");
  });
});

describe("api.deleteComment", () => {
  const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));

  beforeEach(() => {
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // #8296: servers that keep a deleted comment's replies also route
  // /keep-replies; older servers do not, so a keep-replies delete that reaches
  // one fails instead of deleting the replies too.
  it.each([
    [{ keepReplies: true }, "https://api.example.test/api/comments/comment-1/keep-replies"],
    [{ keepReplies: false }, "https://api.example.test/api/comments/comment-1"],
    [undefined, "https://api.example.test/api/comments/comment-1"],
  ])("with %j sends DELETE %s", async (opts, url) => {
    await api.deleteComment("comment-1", opts);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(url, expect.objectContaining({ method: "DELETE" }));
  });
});


describe("api.listIssues requester compatibility", () => {
  afterEach(() => vi.unstubAllGlobals());
  it.each([false, true])("checks server support before returning Created rows (supported=%s)", async (supported) => {
    const fetchMock = vi.fn(async (_url: string) => Response.json({ issues: [], total: 0, ...(supported ? { created_by_user_id: "me" } : {}) }));
    vi.stubGlobal("fetch", fetchMock);
    await api.listIssues({ created_by_user_id: "me" });
    expect(fetchMock).toHaveBeenCalledTimes(supported ? 1 : 2);
    if (!supported) expect(String(fetchMock.mock.calls[1]?.[0])).toContain("creator_id=me");
  });
});

describe("api.sendChatMessage steering", () => {
  const accepted = {
    message_id: "message-1",
    task_id: "task-1",
    created_at: "2026-11-16T00:00:00Z",
  };

  afterEach(() => vi.unstubAllGlobals());

  // The server decides whether a send can join the running reply; the client
  // only states intent. Default sends ask to steer, the explicit queue does not.
  it.each([
    [undefined, true],
    [{ attachmentIds: ["attachment-1"] }, true],
    [{ queue: false }, true],
    [{ queue: true }, false],
  ])("with %j sends steer=%s", async (opts, steer) => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
      Response.json(accepted, { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await api.sendChatMessage("session-1", "use the new path", opts);

    const body = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body));
    expect(body.steer).toBe(steer);
    expect(body.content).toBe("use the new path");
  });
});
