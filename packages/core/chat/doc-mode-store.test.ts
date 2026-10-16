import { beforeEach, describe, expect, it } from "vitest";
import {
  selectActiveDocument,
  selectDiffView,
  selectWorking,
  useDocModeStore,
} from "./doc-mode-store";

beforeEach(() => {
  useDocModeStore.setState({
    activeBySession: {},
    workingBySession: {},
    diffViewBySession: {},
  });
});

describe("useDocModeStore.openDoc", () => {
  it("stores the active document per session (namespaced)", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1", label: "MUL-1" });
    openDoc("session-b", { kind: "attachment", id: "attach-1", contentType: "image/png" });

    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toMatchObject({
      kind: "issue",
      id: "issue-1",
    });
    expect(selectActiveDocument("session-b")(useDocModeStore.getState())).toMatchObject({
      kind: "attachment",
      id: "attach-1",
      contentType: "image/png",
    });
    // Other sessions stay untouched.
    expect(selectActiveDocument("session-c")(useDocModeStore.getState())).toBeNull();
  });

  it("replaces the active document in the same session", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1" });
    openDoc("session-a", { kind: "attachment", id: "attach-9" });
    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toMatchObject({
      kind: "attachment",
      id: "attach-9",
    });
  });
});

describe("useDocModeStore.closeDoc", () => {
  it("clears the active document and the working flag", () => {
    const { openDoc, setWorking, closeDoc } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1" });
    setWorking("session-a", true);
    closeDoc("session-a");

    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toBeNull();
    expect(selectWorking("session-a")(useDocModeStore.getState())).toBe(false);
  });
});

describe("useDocModeStore.setWorking (D9: no auto-switch)", () => {
  it("an edit completing (working -> false) does NOT close doc mode", () => {
    const { openDoc, setWorking } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1" });
    setWorking("session-a", true);
    // The agent finishes editing; doc mode must persist (no auto-switch to chat).
    setWorking("session-a", false);

    expect(selectWorking("session-a")(useDocModeStore.getState())).toBe(false);
    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toMatchObject({
      id: "issue-1",
    });
  });
});

describe("useDocModeStore.setDiffView (D6: off by default)", () => {
  it("diff view defaults to off and toggles only on explicit set", () => {
    expect(selectDiffView("session-a")(useDocModeStore.getState())).toBe(false);
    const { setDiffView } = useDocModeStore.getState();
    setDiffView("session-a", true);
    expect(selectDiffView("session-a")(useDocModeStore.getState())).toBe(true);
    setDiffView("session-a", false);
    expect(selectDiffView("session-a")(useDocModeStore.getState())).toBe(false);
  });
});

describe("selectors", () => {
  it("null sessionId yields stable null/false, not a new object each call", () => {
    const a = selectActiveDocument(null)(useDocModeStore.getState());
    const b = selectActiveDocument(null)(useDocModeStore.getState());
    expect(a).toBe(b);
    expect(a).toBeNull();
    expect(selectWorking(null)(useDocModeStore.getState())).toBe(false);
    expect(selectDiffView(null)(useDocModeStore.getState())).toBe(false);
  });
});
