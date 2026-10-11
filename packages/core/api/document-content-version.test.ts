import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import {
  DocumentContentVersionSchema,
  DocumentContentVersionListSchema,
} from "./document-content-version";

afterEach(() => {
  vi.unstubAllGlobals();
});

function okResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    statusText: status === 200 ? "OK" : "",
    headers: { "Content-Type": "application/json" },
  });
}

const VALID_VERSION = {
  id: "01a12894-5996-7daf-8c7b-f39085ec2812",
  owner_type: "issue",
  owner_id: "01a12894-5996-7daf-8c7b-f39085ec2812",
  revision: 3,
  content_type: "text/markdown",
  content: "# Edited body",
  size_bytes: 12,
  actor_type: "agent",
  actor_id: "7e5aa08a-834f-4b22-8934-d788b4bf64b9",
  merged: false,
  note: null,
  created_at: "2026-10-11T00:00:00Z",
};

const VALID_BINARY = {
  id: "01a12894-5996-7daf-8c7b-f39085ec2813",
  owner_type: "attachment",
  owner_id: "01a12894-5996-7daf-8c7b-f39085ec2813",
  revision: 2,
  content_type: "application/pdf",
  content: null,
  content_url: "/blobs/abc",
  size_bytes: 2048,
  actor_type: "member",
  actor_id: "7e5aa08a-834f-4b22-8934-d788b4bf64b9",
  merged: false,
  note: null,
  created_at: "2026-10-11T00:00:00Z",
};

describe("DocumentContentVersion schema", () => {
  it("applies defaults for a minimal text version", () => {
    const parsed = DocumentContentVersionSchema.parse({
      id: "01a12894-5996-7daf-8c7b-f39085ec2812",
      owner_type: "issue",
      owner_id: "01a12894-5996-7daf-8c7b-f39085ec2812",
      revision: 1,
      content_type: "text/markdown",
      content: "body",
      actor_type: "member",
      actor_id: "7e5aa08a-834f-4b22-8934-d788b4bf64b9",
      created_at: "2026-10-11T00:00:00Z",
    });
    expect(parsed.size_bytes).toBe(0);
    expect(parsed.merged).toBe(false);
    expect(parsed.note).toBeNull();
    expect(parsed.content_url).toBeNull();
  });

  it("keeps a binary version's content_url and nulls the text body", () => {
    const parsed = DocumentContentVersionSchema.parse(VALID_BINARY);
    expect(parsed.content_url).toBe("/blobs/abc");
    expect(parsed.content).toBeNull();
    expect(parsed.owner_type).toBe("attachment");
  });

  it("rejects a non-positive revision and a bad owner type", () => {
    expect(() =>
      DocumentContentVersionSchema.parse({ ...VALID_VERSION, revision: 0 }),
    ).toThrow();
    expect(() =>
      DocumentContentVersionSchema.parse({ ...VALID_VERSION, owner_type: "task" }),
    ).toThrow();
  });
});

describe("ApiClient document content versions", () => {
  it("GETs the current issue version", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse(VALID_VERSION));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const version = await client.getIssueContentVersionCurrent("issue-1");
    expect(version.revision).toBe(3);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/issues/issue-1/content-versions/current",
    );
  });

  it("records an issue version and returns the created row", async () => {
    const created = { ...VALID_VERSION, revision: 4, content: "# merged" };
    const fetchMock = vi.fn().mockResolvedValue(okResponse(created, 201));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const version = await client.recordIssueContentVersion("issue-1", {
      content_type: "text/markdown",
      content: "# merged",
      expected_revision: 3,
      merge: true,
    });
    expect(version.revision).toBe(4);
    const init = fetchMock.mock.calls[0]?.[1] as {
      method?: string;
      body?: string;
    };
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body ?? "{}")).toMatchObject({
      content_type: "text/markdown",
      expected_revision: 3,
      merge: true,
    });
  });

  it("returns [] for a list when the response is malformed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse("not-an-array"));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const list = await client.listIssueContentVersions("issue-1");
    expect(list).toEqual([]);
  });

  it("rolls back an attachment version as a new version", async () => {
    const rolled = { ...VALID_BINARY, revision: 3, content_url: "/blobs/old" };
    const fetchMock = vi.fn().mockResolvedValue(okResponse(rolled));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const version = await client.rollbackAttachmentContentVersion("att-1", 2);
    expect(version.revision).toBe(3);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/attachments/att-1/content-versions/2/rollback",
    );
  });

  it("throws on a 404 so the UI sees a real failure, not a null", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse({ error: "nope" }, 404));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    await expect(client.getIssueContentVersion("issue-1", 99)).rejects.toThrow();
  });

  it("parses a full version list", () => {
    const list = DocumentContentVersionListSchema.parse([
      VALID_VERSION,
      { ...VALID_BINARY },
    ]);
    expect(list).toHaveLength(2);
    expect(list.at(0)?.content).toBe("# Edited body");
    expect(list.at(1)?.content_url).toBe("/blobs/abc");
  });
});
