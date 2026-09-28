import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { LocalVoiceAdapter, LocalVoiceStatus } from "@multica/core/platform";
import { RESOURCES } from "../../test/i18n";
import { ChatVoiceInput } from "./chat-voice-input";

const mocks = vi.hoisted(() => ({
  mutateAsync: vi.fn(), reset: vi.fn(), retry: vi.fn(),
  local: { adapter: null as LocalVoiceAdapter | null, status: null as LocalVoiceStatus | null },
}));
vi.mock("@multica/core/chat/mutations", () => ({
  useTranscribeChatVoice: () => ({ mutateAsync: mocks.mutateAsync, reset: mocks.reset, error: null }),
}));
vi.mock("../../platform", () => ({ useLocalVoice: () => mocks.local }));

const stopTrack = vi.fn();
const getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop: stopTrack }] }));
const draft = { update: vi.fn(), finish: vi.fn(), cancel: vi.fn() };
const activeChange = vi.fn();
let resolve: (text: string) => void;
let partial: (text: string) => void;
const streamStop = vi.fn();
function adapter(): LocalVoiceAdapter {
  return {
    getSnapshot: () => ({ phase: "ready" }), subscribe: () => () => undefined,
    retry: mocks.retry, transcribe: vi.fn(),
    transcribeStream: (_stream, _signal, onPartial) => {
      partial = onPartial;
      return { stop: streamStop, promise: new Promise<string>((done) => { resolve = done; }) };
    },
  };
}
function view() {
  return <I18nProvider locale="en" resources={RESOURCES}>
    <ChatVoiceInput agentId="agent" disabled={false} onBegin={() => draft} onActiveChange={activeChange} />
  </I18nProvider>;
}
class Recorder {
  static isTypeSupported() { return true; }
  state = "inactive";
  mimeType = "audio/webm";
  ondataavailable?: (event: { data: Blob }) => void;
  onstop?: () => void;
  start() { this.state = "recording"; }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob(["synthetic recording"]) });
    this.onstop?.();
  }
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
  vi.stubGlobal("MediaRecorder", Recorder);
  mocks.local = { adapter: adapter(), status: { phase: "ready" } };
});
afterEach(() => vi.unstubAllGlobals());

describe("ChatVoiceInput", () => {
  it("one click starts live dictation, Done keeps final text in the draft", async () => {
    render(view());
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByText("Listening · speech preview is editable after Done");
    expect(getUserMedia).toHaveBeenCalledOnce();
    act(() => partial("live text"));
    expect(draft.update).toHaveBeenCalledWith("live text");
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.getByText("Finishing transcription...")).toBeInTheDocument();
    await act(async () => resolve("Final text"));
    expect(draft.update).toHaveBeenLastCalledWith("Final text");
    expect(draft.finish).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Start dictation" })).toHaveFocus();
    expect(activeChange.mock.calls).toEqual([[true], [false]]);
  });

  it("supports Enter/Space activation and Escape cancellation", async () => {
    const user = userEvent.setup();
    render(view());
    screen.getByRole("button", { name: "Start dictation" }).focus();
    await user.keyboard("{Enter}");
    await screen.findByRole("button", { name: "Done" });
    await user.keyboard("{Escape}");
    expect(draft.cancel).toHaveBeenCalledOnce();
    await user.keyboard(" ");
    await screen.findByRole("button", { name: "Done" });
    await user.click(screen.getByRole("button", { name: "Cancel dictation (Esc)" }));
    expect(draft.cancel).toHaveBeenCalledTimes(2);
  });

  it("shows setup progress and retries a failed model without requesting microphone access", () => {
    mocks.local.status = { phase: "downloading", percent: 48 };
    const { rerender } = render(view());
    expect(screen.getByRole("button", { name: "Preparing voice 48%" })).toBeDisabled();
    mocks.local.status = { phase: "failed", percent: 48 };
    rerender(view());
    fireEvent.click(screen.getByRole("button", { name: "Retry voice setup" }));
    expect(mocks.retry).toHaveBeenCalledOnce();
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("keeps the existing batch path truthful and transcribes after Done", async () => {
    mocks.local = { adapter: null, status: null };
    mocks.mutateAsync.mockResolvedValue("Batch result");
    render(view());
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByText("Recording · text appears after Done");
    expect(mocks.mutateAsync).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(draft.finish).toHaveBeenCalledOnce());
    expect(mocks.mutateAsync).toHaveBeenCalledWith(expect.objectContaining({ agentId: "agent", recording: expect.any(Blob), signal: expect.any(AbortSignal) }));
    expect(draft.update).toHaveBeenCalledWith("Batch result");
    expect(stopTrack).toHaveBeenCalled();
  });
});
