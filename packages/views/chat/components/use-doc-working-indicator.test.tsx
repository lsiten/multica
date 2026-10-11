// @vitest-environment jsdom

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { useState } from "react";
import {
  useDocModeStore,
  selectActiveDocument,
  selectWorking,
} from "@multica/core/chat";
import { useDocWorkingIndicator } from "./use-doc-working-indicator";

beforeEach(() => {
  useDocModeStore.setState({
    activeByWorkspace: {},
    workingByWorkspace: {},
    diffViewByWorkspace: {},
  });
});

afterEach(() => {
  cleanup();
});

function Harness({ initial }: { initial: boolean }) {
  const [isAgentWorking, toggle] = useState(initial);
  useDocWorkingIndicator("ws-a", isAgentWorking);
  return (
    <button type="button" onClick={() => toggle((v) => !v)}>
      toggle
    </button>
  );
}

function working() {
  return selectWorking("ws-a")(useDocModeStore.getState());
}

describe("useDocWorkingIndicator (D7)", () => {
  it("sets working only when a doc is open AND the agent is working", () => {
    useDocModeStore.getState().openDoc("ws-a", { kind: "issue", id: "issue-1" });
    const { container } = render(<Harness initial={false} />);
    const button = container.querySelector("button")!;

    // Doc open but the agent is idle: not working.
    expect(working()).toBe(false);

    // The agent starts working on the document.
    act(() => fireEvent.click(button));
    expect(working()).toBe(true);
  });

  it("clears working when the agent finishes but keeps doc mode (D9)", () => {
    useDocModeStore.getState().openDoc("ws-a", { kind: "issue", id: "issue-1" });
    const { container } = render(<Harness initial={true} />);
    const button = container.querySelector("button")!;
    // The agent is working.
    expect(working()).toBe(true);

    // The agent finishes: the working flag clears, doc mode must persist.
    act(() => fireEvent.click(button));
    expect(working()).toBe(false);
    expect(selectActiveDocument("ws-a")(useDocModeStore.getState())).toMatchObject({
      id: "issue-1",
    });
  });

  it("does not set working when no document is open", () => {
    render(<Harness initial={true} />);
    // The agent is working, but no document is open in doc mode.
    expect(working()).toBe(false);
  });
});
