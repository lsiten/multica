import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import {
  ApplyRuntimeFileEditRequestSchema,
  RuntimeFileEditResultSchema,
  parseRuntimeFileEditResult,
} from "./runtime-file-edit";

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

const base = {
  task_id: "task-1",
  path: "/runtime/repo",
  target: "main",
  relative_path: "file.txt",
  command_id: "cmd-1",
};

describe("ApplyRuntimeFileEditRequestSchema", () => {
  it("accepts a compare-and-swap edit with a base digest", () => {
    const parsed = ApplyRuntimeFileEditRequestSchema.parse({
      ...base,
      action: "edit",
      edit_base_sha: "a".repeat(64),
      edit_new: "edited\n",
    });
    expect(parsed.action).toBe("edit");
    expect(parsed.edit_base_sha).toBe("a".repeat(64));
  });

  it("accepts a three-way merge with a base body", () => {
    const parsed = ApplyRuntimeFileEditRequestSchema.parse({
      ...base,
      action: "merge_edit",
      edit_base: "line one\n",
      edit_new: "line one edited\n",
    });
    expect(parsed.action).toBe("merge_edit");
    expect(parsed.edit_base).toBe("line one\n");
  });

  it("rejects an edit without a base digest and a merge without a base body", () => {
    expect(() =>
      ApplyRuntimeFileEditRequestSchema.parse({ ...base, action: "edit", edit_new: "x" }),
    ).toThrow();
    expect(() =>
      ApplyRuntimeFileEditRequestSchema.parse({ ...base, action: "merge_edit", edit_new: "x" }),
    ).toThrow();
  });

  it("rejects a malformed base digest and an over-long path", () => {
    expect(() =>
      ApplyRuntimeFileEditRequestSchema.parse({
        ...base,
        action: "edit",
        edit_base_sha: "tooshort",
        edit_new: "x",
      }),
    ).toThrow();
    expect(() =>
      ApplyRuntimeFileEditRequestSchema.parse({
        ...base,
        action: "edit",
        edit_base_sha: "a".repeat(64),
        relative_path: "x".repeat(5000),
        edit_new: "x",
      }),
    ).toThrow();
  });
});

describe("RuntimeFileEditResultSchema", () => {
  it("applies defaults for a bare edit result", () => {
    const parsed = RuntimeFileEditResultSchema.parse({
      file_path: "file.txt",
      new_sha256: "b".repeat(64),
    });
    expect(parsed.merged).toBe(false);
    expect(parsed.conflict).toBe(false);
    expect(parsed.bytes).toBe(0);
    expect(parsed.new_content).toBeUndefined();
  });

  it("keeps a conflict result with the runtime version", () => {
    const parsed = RuntimeFileEditResultSchema.parse({
      file_path: "file.txt",
      conflict: true,
      new_content: "B\n",
      current_sha256: "c".repeat(64),
    });
    expect(parsed.conflict).toBe(true);
    expect(parsed.new_content).toBe("B\n");
  });
});

describe("parseRuntimeFileEditResult", () => {
  it("reads the result from the relayed edit envelope", () => {
    const parsed = parseRuntimeFileEditResult({
      edit: { file_path: "file.txt", new_sha256: "b".repeat(64), bytes: 12 },
    });
    expect(parsed?.new_sha256).toBe("b".repeat(64));
    expect(parsed?.bytes).toBe(12);
  });

  it("accepts an unwrapped result (desktop direct path)", () => {
    const parsed = parseRuntimeFileEditResult({
      file_path: "file.txt",
      merged: true,
      new_content: "line one merged\n",
    });
    expect(parsed?.merged).toBe(true);
  });

  it("degrades a malformed result to null", () => {
    expect(parseRuntimeFileEditResult({ edit: { file_path: 42 } })).toBeNull();
  });
});

describe("ApiClient.applyRuntimeFileEdit", () => {
  it("POSTs the edit command and returns the parsed result", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okResponse({ edit: { file_path: "file.txt", new_sha256: "b".repeat(64), bytes: 12 } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const result = await client.applyRuntimeFileEdit({
      ...base,
      action: "edit",
      edit_base_sha: "a".repeat(64),
      edit_new: "edited\n",
    });
    expect(result?.new_sha256).toBe("b".repeat(64));
    const init = fetchMock.mock.calls[0]?.[1] as { method?: string; body?: string };
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body ?? "{}")).toMatchObject({
      action: "edit",
      relative_path: "file.txt",
      edit_base_sha: "a".repeat(64),
      edit_new: "edited\n",
    });
  });

  it("throws on a 409 so the UI reloads off the fresh content", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okResponse({ error: "working file changed since the base; reload before editing" }, 409),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.applyRuntimeFileEdit({
        ...base,
        action: "edit",
        edit_base_sha: "a".repeat(64),
        edit_new: "edited\n",
      }),
    ).rejects.toThrow();
  });

  it("returns a conflict result (runtime wins) without throwing", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okResponse({ edit: { file_path: "file.txt", conflict: true, new_content: "B\n" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    const result = await client.applyRuntimeFileEdit({
      ...base,
      action: "merge_edit",
      edit_base: "X\n",
      edit_new: "C\n",
    });
    expect(result?.conflict).toBe(true);
    expect(result?.new_content).toBe("B\n");
  });
});
