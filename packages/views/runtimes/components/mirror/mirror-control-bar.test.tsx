import { fireEvent, screen, waitFor } from "@testing-library/react";
import type { ComponentProps } from "react";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../../../test/i18n";
import { MirrorControlBar } from "./mirror-control-bar";

vi.mock("./mirror-voice-input", () => ({
  MirrorVoiceInput: ({ onTranscript }: { onTranscript: (text: string) => void }) =>
    <button onClick={() => onTranscript("Recognized words")}>Finish transcription</button>,
}));

describe("MirrorControlBar voice draft", () => {
  it("preserves typed text, fills an editable transcript, and sends only on confirmation", async () => {
    const onAgentMessage = vi.fn(async () => undefined);
    const props: ComponentProps<typeof MirrorControlBar> = {
      scope: { backendIdentity: "https://fixture.invalid", accountId: "owner", workspaceId: "workspace", runtimeId: "runtime" },
      runtime: {
        id: "runtime", workspace_id: "workspace", owner_id: "owner", visibility: "private",
        status: "online", daemon_id: "daemon", name: "Fixture runtime", runtime_mode: "local",
        provider: "fixture", launch_header: "", device_info: "", metadata: {},
        last_seen_at: null, created_at: "2026-09-21", updated_at: "2026-09-21",
      },
      source: null, state: null, videoReady: true, control: { status: "inactive" },
      agents: [{ id: "agent", name: "Agent" }], agentId: "agent", onAgentMessage,
      onCommand: vi.fn(), onStartControl: vi.fn(), onStopControl: vi.fn(), onType: vi.fn(), onKey: vi.fn(), onVoice: vi.fn(),
      voiceTranscript: "Previous recording",
    };
    renderWithI18n(<MirrorControlBar {...props} />);
    expect(screen.getByRole("button", { name: "Type text" })).toBeDisabled();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Existing draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Use voice input" }));
    fireEvent.click(screen.getByRole("button", { name: "Finish transcription" }));
    expect(screen.getByRole("textbox")).toHaveValue("Existing draft Recognized words");
    expect(onAgentMessage).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Reviewed transcript" } });
    fireEvent.click(screen.getByRole("button", { name: "Type text" }));
    await waitFor(() => expect(onAgentMessage).toHaveBeenCalledWith("agent", "Reviewed transcript"));
    expect(screen.getByRole("textbox")).toHaveValue("");
    expect(screen.getByRole("button", { name: "Type text" })).toBeDisabled();
  });
});
