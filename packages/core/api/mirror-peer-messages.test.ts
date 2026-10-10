// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseMirrorAuthorizationRequest, parseMirrorAuthorizationResult } from "./mirror-peer-messages";

describe("mirror approval result boundary", () => {
  it.each(["mcp", "future_operation"])("preserves MCP approvals while blocking unsupported operation %s", (kind) => {
    const request = parseMirrorAuthorizationRequest(JSON.stringify({
      type: "mirror-authorization:request", request_id: "request", kind: "cli",
      title: "Codex operation approval", message: "Review the tool call",
      expires_at: "2026-10-08T05:00:00Z",
      operation: { kind, target: "multica-llm2jev", details: "{}" },
    }), Date.parse("2026-10-08T04:00:00Z"));
    expect(request?.operation?.kind).toBe(kind === "mcp" ? "mcp" : "unknown");
  });
  it("preserves a negative result without interpreting it as approval", () => {
    expect(parseMirrorAuthorizationResult(JSON.stringify({
      type: "mirror-authorization:result", request_id: "request", processed: false,
    }))).toEqual({type: "mirror-authorization:result", request_id: "request", processed: false});
  });
  it.each([
    "not json",
    JSON.stringify({type: "mirror-authorization:result", request_id: "request", processed: "true"}),
    JSON.stringify({type: "mirror-authorization:result", request_id: "", processed: true}),
    JSON.stringify({type: "mirror-authorization:result", request_id: "request"}),
  ])("rejects malformed results", (value) => {
    expect(parseMirrorAuthorizationResult(value)).toBeNull();
  });
});
