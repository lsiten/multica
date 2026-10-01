import { beforeEach, describe, expect, it } from "vitest";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import { useModalStore } from "./store";

describe("modal workspace ownership", () => {
  beforeEach(() => {
    useModalStore.getState().close();
    setCurrentWorkspace("alpha", "ws-alpha");
  });

  it("captures the workspace at open time", () => {
    useModalStore.getState().open("create-squad");
    expect(useModalStore.getState().workspaceId).toBe("ws-alpha");
  });

  it("clears ownership together with the modal", () => {
    useModalStore.getState().open("create-project");
    useModalStore.getState().close();
    expect(useModalStore.getState()).toMatchObject({
      modal: null,
      data: null,
      workspaceId: null,
    });
  });
});
