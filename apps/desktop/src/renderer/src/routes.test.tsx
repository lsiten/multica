import { isValidElement } from "react";
import { matchRoutes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { appRoutes } from "./routes";
import { RuntimeMirrorPage } from "./pages/runtime-detail-page";

describe("desktop runtime mirror route", () => {
  it("resolves a workspace mirror URL to the runtime mirror page", () => {
    const match = matchRoutes(appRoutes, "/acme/runtimes/runtime-1/mirror")?.at(-1);

    expect(match?.params).toEqual({ workspaceSlug: "acme", id: "runtime-1" });
    expect(isValidElement(match?.route.element) && match.route.element.type)
      .toBe(RuntimeMirrorPage);
  });
});
