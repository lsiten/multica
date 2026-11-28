import { useEffect, useRef } from "react";
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useWorkflowDraft } from "./use-workflow-draft";
import type { Draft } from "./workflow-draft";

const original: Draft = { name: "Original", description: "", backlog: true, blocked: true, middle: [], handoffs: {} };

describe("workflow draft baselines", () => {
  it("settles when catalog reconciliation returns the current draft", () => {
    const { result } = renderHook(() => {
      const draft = useWorkflowDraft(original, "workflow-1", "v1");
      const renders = useRef(0);
      if (++renders.current > 10) throw new Error("No-op reconciliation did not settle");
      useEffect(() => { draft.setDraft((current) => current); });
      return draft;
    });

    expect(result.current.draft).toEqual(original);
    expect(result.current.dirty).toBe(false);
    expect(result.current.remoteChanged).toBe(false);
  });

  it("keeps the acknowledged save while the query still contains the previous version", () => {
    const { result, rerender } = renderHook(({ source, version }) => useWorkflowDraft(source, "workflow-1", version), { initialProps: { source: original, version: "v1" } });
    const saved = { ...original, name: "Saved change" };

    act(() => { result.current.setDraft(saved); result.current.commit(saved, "v2"); });
    rerender({ source: { ...original }, version: "v1" });

    expect(result.current.draft.name).toBe("Saved change");
    expect(result.current.version).toBe("v2");
    expect(result.current.dirty).toBe(false);
    expect(result.current.remoteChanged).toBe(false);
  });

  it("reloads the latest remote version only after the caller explicitly accepts it", () => {
    const { result, rerender } = renderHook(({ source, version }) => useWorkflowDraft(source, "workflow-1", version), { initialProps: { source: original, version: "v1" } });
    act(() => result.current.setDraft({ ...original, name: "Local input" }));
    const remote = { ...original, name: "Other editor" };
    rerender({ source: remote, version: "v2" });
    expect(result.current.version).toBe("v1");
    expect(result.current.remoteChanged).toBe(true);

    act(() => result.current.reload());

    expect(result.current.draft).toEqual(remote);
    expect(result.current.version).toBe("v2");
    expect(result.current.dirty).toBe(false);
  });
});
