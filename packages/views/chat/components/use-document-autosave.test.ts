// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { useDocumentAutosave } from "./use-document-autosave";

function version(revision: number, content: string): DocumentContentVersion {
  return {
    id: `version-${revision}`,
    owner_type: "issue",
    owner_id: "issue-1",
    revision,
    owner_revision: revision,
    content_type: "text/markdown",
    content,
    content_url: null,
    size_bytes: content.length,
    actor_type: "member",
    actor_id: "member-1",
    merged: true,
    note: null,
    created_at: "2026-11-15T00:00:00Z",
  };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("useDocumentAutosave", () => {
  it("merges the idle autosave and keeps explicit checkpoints distinct", async () => {
    vi.useFakeTimers();
    const save = vi.fn(async (input: {
      readonly content: string;
      readonly merge: boolean;
      readonly note?: string;
    }) => version(2, input.merge ? `${input.content}:merged` : input.content));
    const { result, rerender } = renderHook(
      (props: {
        readonly content: string;
        readonly base: {
          readonly content: string;
          readonly revision: number;
          readonly contentType: string;
        };
      }) => useDocumentAutosave({
        enabled: true,
        content: props.content,
        base: props.base,
        save,
        onBaseChange: () => undefined,
        onSaved: () => undefined,
        onConflict: () => undefined,
      }),
      {
        initialProps: {
          content: "# Draft",
          base: {
            content: "# Original",
            revision: 1,
            contentType: "text/markdown",
          },
        },
      },
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      content: "# Draft",
      revision: 1,
      merge: true,
    }));

    rerender({
      content: "# Checkpoint",
      base: {
        content: "# Draft",
        revision: 2,
        contentType: "text/markdown",
      },
    });
    await act(async () => {
      await result.current.saveNow(false, "Ready for review");
    });
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({
      content: "# Checkpoint",
      revision: 2,
      merge: false,
      note: "Ready for review",
    }));
  });
});
