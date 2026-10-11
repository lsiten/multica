import { beforeEach, describe, expect, it } from "vitest";
import {
  selectActiveDocument,
  selectDiffView,
  selectWorking,
  useDocModeStore,
} from "./doc-mode-store";

beforeEach(() => {
  useDocModeStore.setState({
    activeByWorkspace: {},
    workingByWorkspace: {},
    diffViewByWorkspace: {},
  });
});

describe("useDocModeStore.openDoc", () => {
  it("stores the active document per workspace (namespaced)", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("ws-a", { kind: "issue", id: "issue-1", label: "MUL-1" });
    openDoc("ws-b", { kind: "attachment", id: "attach-1", contentType: "image/png" });

    expect(selectActiveDocument("ws-a")(useDocModeStore.getState())).toMatchObject({
      kind: "issue",
      id: "issue-1",
    });
    expect(selectActiveDocument("ws-b")(useDocModeStore.getState())).toMatchObject({
      kind: "attachment",
      id: "attach-1",
      contentType: "image/png",
    });
    // Other workspaces stay untouched.
    expect(selectActiveDocument("ws-c")(useDocModeStore.getState())).toBeNull();
  });

  it("replaces the active document in the same workspace", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("ws-a", { kind: "issue", id: "issue-1" });
    openDoc("ws-a", { kind: "attachment", id: "attach-9" });
    expect(selectActiveDocument("ws-a")(useDocModeStore.getState())).toMatchObject({
      kind: "attachment",
      id: "attach-9",
    });
  });
});

describe("useDocModeStore.closeDoc", () => {
  it("clears the active document and the working flag", () => {
    const { openDoc, setWorking, closeDoc } = useDocModeStore.getState();
    openDoc("ws-a", { kind: "issue", id: "issue-1" });
    setWorking("ws-a", true);
    closeDoc("ws-a");

    expect(selectActiveDocument("ws-a")(useDocModeStore.getState())).toBeNull();
    expect(selectWorking("ws-a")(useDocModeStore.getState())).toBe(false);
  });
});

describe("useDocModeStore.setWorking (D9: no auto-switch)", () => {
  it("an edit completing (working -> false) does NOT close doc mode", () => {
    const { openDoc, setWorking } = useDocModeStore.getState();
    openDoc("ws-a", { kind: "issue", id: "issue-1" });
    setWorking("ws-a", true);
    // The agent finishes editing; doc mode must persist (no auto-switch to chat).
    setWorking("ws-a", false);

    expect(selectWorking("ws-a")(useDocModeStore.getState())).toBe(false);
    expect(selectActiveDocument("ws-a")(useDocModeStore.getState())).toMatchObject({
      id: "issue-1",
    });
  });
});

describe("useDocModeStore.setDiffView (D6: off by default)", () => {
  it("diff view defaults to off and toggles only on explicit set", () => {
    expect(selectDiffView("ws-a")(useDocModeStore.getState())).toBe(false);
    const { setDiffView } = useDocModeStore.getState();
    setDiffView("ws-a", true);
    expect(selectDiffView("ws-a")(useDocModeStore.getState())).toBe(true);
    setDiffView("ws-a", false);
    expect(selectDiffView("ws-a")(useDocModeStore.getState())).toBe(false);
  });
});

describe("selectors", () => {
  it("null wsId yields stable null/false, not a new object each call", () => {
    const a = selectActiveDocument(null)(useDocModeStore.getState());
    const b = selectActiveDocument(null)(useDocModeStore.getState());
    expect(a).toBe(b);
    expect(a).toBeNull();
    expect(selectWorking(null)(useDocModeStore.getState())).toBe(false);
    expect(selectDiffView(null)(useDocModeStore.getState())).toBe(false);
  });
});
