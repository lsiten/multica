import { isValidElement } from "react";
import { matchRoutes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { appRoutes } from "./routes";
import { RuntimeMirrorPage } from "./pages/runtime-detail-page";
import { ApplicationsPage } from "@multica/views/applications";
import { ApplicationDetailPage } from "./pages/application-detail-page";

describe("desktop runtime mirror route", () => {
  it("resolves a workspace mirror URL to the runtime mirror page", () => {
    const match = matchRoutes(appRoutes, "/acme/runtimes/runtime-1/mirror")?.at(-1);

    expect(match?.params).toEqual({ workspaceSlug: "acme", id: "runtime-1" });
    expect(isValidElement(match?.route.element) && match.route.element.type)
      .toBe(RuntimeMirrorPage);
  });
});


describe("desktop application routes", () => {
  it("opens the workspace application list", () => {
    const match = matchRoutes(appRoutes, "/acme/applications")?.at(-1);

    expect(match?.params).toEqual({ workspaceSlug: "acme" });
    expect(isValidElement(match?.route.element) && match.route.element.type)
      .toBe(ApplicationsPage);
  });

  it("opens an application detail in its workspace", () => {
    const match = matchRoutes(appRoutes, "/acme/applications/app-1")?.at(-1);

    expect(match?.params).toEqual({ workspaceSlug: "acme", id: "app-1" });
    expect(isValidElement(match?.route.element) && match.route.element.type)
      .toBe(ApplicationDetailPage);
  });
});
