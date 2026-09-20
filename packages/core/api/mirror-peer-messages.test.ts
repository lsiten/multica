// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseMirrorAuthorizationResult } from "./mirror-peer-messages";

describe("mirror approval result boundary", () => {
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
