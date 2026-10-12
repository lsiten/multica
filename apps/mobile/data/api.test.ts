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
