// @vitest-environment node
import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";
import { mkdir, writeFile } from "node:fs/promises";
import { afterEach, describe, expect, it } from "vitest";
import { ApiClient } from "./client";
import { setApiInstance } from "./index";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import {
  envelope,
  grantWire,
  scope,
  sourceWire,
  stateWire,
} from "./vscreen-fixtures";
import { parseVscreenSources } from "./vscreen-state";
import { VscreenScopeError, vscreenErrorReason } from "./vscreen";

async function serverFor(
  handler: (request: IncomingMessage, response: ServerResponse) => void,
) {
  const server = createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string")
    throw new Error("invalid test address");
  return {
    url: `http://127.0.0.1:${address.port}`,
    close: () =>
      new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      ),
  };
}
const cleanup: (() => Promise<void>)[] = [];
afterEach(async () => {
  setCurrentWorkspace(null, null);
  await Promise.all(cleanup.splice(0).map((close) => close()));
});

function bindingFor(backendIdentity: string) {
  const result = parseVscreenSources(
    {
      ...envelope,
      sources: [
        {
          ...sourceWire,
          resource: {
            ...sourceWire.resource,
            backend_identity: backendIdentity,
          },
        },
      ],
    },
    { ...scope, backendIdentity },
  );
  const binding = result?.sources[0];
  if (!binding) throw new Error("invalid fixture");
  return binding;
}

describe("Vscreen HTTP client", () => {
  it("uses the explicit workspace header when the global route changes", async () => {
    // Given
    let headers: IncomingMessage["headers"] = {};
    let path = "";
    const server = await serverFor((request, response) => {
      headers = request.headers;
      path = request.url ?? "";
      response.end(JSON.stringify(stateWire));
    });
    cleanup.push(server.close);
    setCurrentWorkspace("foreign-slug", "foreign-workspace");
    const client = new ApiClient(server.url).vscreen({
      ...scope,
      backendIdentity: server.url,
    });
    // When
    const result = await client.getState();
    // Then
    expect(headers["x-workspace-id"]).toBe(scope.workspaceId);
    expect(headers["x-workspace-slug"]).toBe("");
    expect(path).toBe("/api/runtimes/runtime-1/vscreen");
    expect(result?.state.stateRevision).toBe(3);
    const evidence = new URL(
      "../../../.omo/evidence/runtime-vscreen/wave3/task-10-api-fixture.json",
      import.meta.url,
    );
    await mkdir(new URL(".", evidence), { recursive: true });
    await writeFile(
      evidence,
      JSON.stringify(
        {
          request: {
            path,
            workspaceId: headers["x-workspace-id"],
            workspaceSlug: headers["x-workspace-slug"],
          },
          wire: stateWire,
          parsed: result,
        },
        null,
        2,
      ),
    );
  });

  it.each(["credential", "account", "backend"])(
    "rejects an in-flight response when %s changes",
    async (change) => {
      // Given
      let respond: (() => void) | undefined;
      let received: (() => void) | undefined;
      const started = new Promise<void>((resolve) => {
        received = resolve;
      });
      const server = await serverFor((_request, response) => {
        respond = () => response.end(JSON.stringify(stateWire));
        received?.();
      });
      cleanup.push(server.close);
      const api = new ApiClient(server.url);
      api.setToken("test-a");
      setApiInstance(api);
      const client = api.vscreen({ ...scope, backendIdentity: server.url });
      // When
      const pending = client.getState();
      await started;
      switch (change) {
        case "account":
          api.vscreen({
            ...scope,
            backendIdentity: server.url,
            accountId: "user-2",
          });
          break;
        case "backend":
          setApiInstance(new ApiClient("https://other.test"));
          break;
        default:
          api.setToken("test-b");
          api.setToken("test-a");
      }
      respond?.();
      // Then
      await expect(pending).rejects.toBeInstanceOf(VscreenScopeError);
    },
  );

  it("sends video source selection and parses renewal without changing the AI state", async () => {
    // Given
    const requests: { path: string; method: string; body: string }[] = [];
    const server = await serverFor((request, response) => {
      let body = "";
      request.on("data", (chunk: Buffer) => {
        body += chunk.toString();
      });
      request.on("end", () => {
        requests.push({
          path: request.url ?? "",
          method: request.method ?? "",
          body,
        });
        response.end(JSON.stringify(grantWire));
      });
    });
    cleanup.push(server.close);
    const viewer = {
      sessionId: "session-1",
      viewerId: "viewer-1",
      binding: bindingFor(server.url),
    };
    const client = new ApiClient(server.url).vscreen({
      ...scope,
      backendIdentity: server.url,
    });
    // When
    const grant = await client.renewMirrorSession(viewer);
    // Then
    expect(requests).toEqual([
      {
        path: "/api/runtimes/runtime-1/mirror/sessions/session-1/renew",
        method: "POST",
        body: "",
      },
    ]);
    expect(grant?.sourceGeneration).toBe("capture-1");
  });

  it.each([400, 403, 404, 409, 501, 503, 504])(
    "preserves a machine-readable reason when HTTP returns %s",
    async (status) => {
      // Given
      const server = await serverFor((_request, response) => {
        response.statusCode = status;
        response.end(JSON.stringify({ reason: "permission_denied" }));
      });
      cleanup.push(server.close);
      const client = new ApiClient(server.url).vscreen({
        ...scope,
        backendIdentity: server.url,
      });
      // When
      const result = await client.getState().catch((error: unknown) => error);
      // Then
      expect(vscreenErrorReason(result)).toBe("permission_denied");
    },
  );
});
