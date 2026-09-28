import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { attachmentToDraftUpload, type DraftUpload } from "@multica/core/drafts";
import type { Attachment, ChatMessage } from "@multica/core/types";
import { useChatInputHistory } from "./use-chat-input-history";

function message(id: string, content = id, overrides: Partial<ChatMessage> = {}): ChatMessage {
  return { id, content, role: "user", chat_session_id: "session", task_id: null, created_at: "", ...overrides };
}

const attachment: Attachment = {
  id: "file", filename: "report.pdf", workspace_id: "workspace", issue_id: null,
  comment_id: null, chat_session_id: "session", chat_message_id: "older",
  uploader_type: "member", uploader_id: "user", url: "https://cdn.test/report.pdf",
  download_url: "https://cdn.test/report.pdf?signature=expired", markdown_url: "/api/attachments/file/download",
  content_type: "application/pdf", size_bytes: 10, created_at: "",
};

function setup(messages: ChatMessage[] = [message("older"), message("latest")]) {
  const hook = renderHook(useChatInputHistory, {
    initialProps: { scope: "workspace:session", sessionId: "session", messages },
  });
  let markdown = "";
  let empty = true;
  let uploads: DraftUpload[] = [];
  return {
    ...hook,
    content: () => markdown,
    uploads: () => uploads,
    edit: (value: string, isEmpty = value === "") => { markdown = value; empty = isEmpty; },
    setUploads: (value: DraftUpload[]) => { uploads = value; },
    press: (direction: "previous" | "next") => {
      let handled = false;
      act(() => {
        handled = hook.result.current.navigate({ direction, markdown, empty, uploads }, (draft) => {
          markdown = draft.content;
          empty = draft.content === "";
          uploads = draft.attachments.map(attachmentToDraftUpload);
          return markdown;
        });
      });
      return handled;
    },
  };
}

describe("chat input history", () => {
  it("walks loaded user inputs, consumes the oldest boundary and returns to an empty draft", () => {
    const history = setup([
      message("older"), message("reply", "assistant", { role: "assistant" }),
      message("hidden", "kickoff", { message_kind: "onboarding_kickoff" }),
      message("foreign", "private", { chat_session_id: "other" }), message("latest"),
    ]);
    expect(history.press("next")).toBe(false);
    history.press("previous");
    expect(history.content()).toBe("latest");
    history.press("previous");
    expect(history.content()).toBe("older");
    expect(history.press("previous")).toBe(true);
    expect(history.content()).toBe("older");
    history.press("next");
    expect(history.content()).toBe("latest");
    history.press("next");
    expect(history.content()).toBe("");
  });

  it("preserves fresh drafts, including whitespace lost by Markdown serialization", () => {
    const history = setup();
    history.edit("new draft");
    expect(history.press("previous")).toBe(false);
    history.edit("", false);
    expect(history.press("previous")).toBe(false);
  });

  it("stops navigation after an edit and starts at the latest after clearing", () => {
    const history = setup();
    history.press("previous");
    history.edit("latest edited");
    expect(history.press("previous")).toBe(false);
    expect(history.content()).toBe("latest edited");
    history.edit("");
    history.press("previous");
    expect(history.content()).toBe("latest");
  });

  it("keeps its position by message ID when earlier pages and realtime messages arrive", () => {
    const history = setup();
    history.press("previous");
    history.rerender({ scope: "workspace:session", sessionId: "session", messages: [message("first"), message("older"), message("latest"), message("new")] });
    history.press("previous");
    expect(history.content()).toBe("older");
    history.press("next");
    history.press("next");
    expect(history.content()).toBe("new");
  });

  it("resets navigation when its session or workspace changes", () => {
    const history = setup();
    history.press("previous");
    history.rerender({ scope: "other:session", sessionId: "session", messages: [message("new")] });
    expect(history.press("previous")).toBe(false);
    expect(history.content()).toBe("latest");
    history.edit("");
    history.press("previous");
    expect(history.content()).toBe("new");
  });

  it("protects existing attachments and pending upload placeholders", () => {
    const history = setup();
    history.setUploads([attachmentToDraftUpload(attachment)]);
    expect(history.press("previous")).toBe(false);
    history.setUploads([{ clientUploadId: "pending", filename: "new.pdf", status: "uploading", size: 10 }]);
    expect(history.press("previous")).toBe(false);
  });

  it("restores attachment-only history with a stable link, then removes its draft binding on return", () => {
    const history = setup([message("older", "", { attachments: [attachment] })]);
    history.press("previous");
    expect(history.content()).toBe("[report.pdf](/api/attachments/file/download)");
    expect(history.uploads()).toMatchObject([{ attachment: { id: "file", chat_message_id: "older" } }]);
    history.press("next");
    expect(history.content()).toBe("");
    expect(history.uploads()).toEqual([]);
  });

  it("replaces an expiring historical attachment link before its metadata is dropped on send", () => {
    const history = setup([message("older", `[report.pdf](${attachment.download_url})`, { attachments: [attachment] })]);
    history.press("previous");
    expect(history.content()).toBe("[report.pdf](/api/attachments/file/download)");
  });

  it("does not overwrite a recalled draft when its message disappears", () => {
    const history = setup();
    history.press("previous");
    history.rerender({ scope: "workspace:session", sessionId: "session", messages: [] });
    expect(history.press("previous")).toBe(false);
    expect(history.content()).toBe("latest");
  });

  it("adds a durable reference when historical and current attachment signatures differ", () => {
    const history = setup([message("older", "[report.pdf](https://cdn.test/report.pdf?signature=older)", { attachments: [attachment] })]);
    history.press("previous");
    expect(history.content()).toContain("[report.pdf](/api/attachments/file/download)");
  });
});
