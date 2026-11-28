import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SupportedLocale } from "@multica/core/i18n";
import { ApiError } from "@multica/core/api";
import { I18nProvider } from "@multica/core/i18n/react";
import type { LocalVoiceAdapter, LocalVoiceStatus } from "@multica/core/platform";
import { getTestResources } from "../../test/i18n";
import { ChatVoiceInput } from "./chat-voice-input";

const mocks = vi.hoisted(() => ({
  mutateAsync: vi.fn(), reset: vi.fn(), retry: vi.fn(),
  meter: { notify: (_level: number | null) => {} },
  local: { adapter: null as LocalVoiceAdapter | null, status: null as LocalVoiceStatus | null },
}));
vi.mock("@multica/core/chat/mutations", () => ({
  useTranscribeChatVoice: () => ({ mutateAsync: mocks.mutateAsync, reset: mocks.reset, error: null }),
}));
vi.mock("../../platform", () => ({ useLocalVoice: () => mocks.local }));

vi.mock("./dictation-volume", () => ({
  prepareDictationVolume: (notify: (level: number | null) => void) => {
    mocks.meter.notify = notify;
    return { connect() {}, close() {} };
  },
}));

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
function view(locale: SupportedLocale = "en") {
  return <I18nProvider locale={locale} resources={getTestResources(locale)}>
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
  pause() { this.state = "paused"; }
  resume() { this.state = "recording"; }
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
  it("uses the chosen Simplified Chinese script for partial and final dictation", async () => {
    render(view("zh-Hans"));
    fireEvent.click(screen.getByRole("button", { name: "开始语音输入" }));
    await waitFor(() => expect(getUserMedia).toHaveBeenCalled());
    act(() => partial("我們測試語音"));
    expect(draft.update).toHaveBeenLastCalledWith("我们测试语音");
    await act(async () => resolve("我們測試語音輸入。"));
    expect(draft.update).toHaveBeenLastCalledWith("我们测试语音输入。");
  });

  it("reports unreliable recognition without inserting a transcript", async () => {
    mocks.local = { adapter: null, status: null };
    mocks.mutateAsync.mockRejectedValueOnce(new ApiError("unreliable_transcript", 503, "Service Unavailable"));
    render(view());
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    fireEvent.click(await screen.findByRole("button", { name: "Done" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("could not be recognized reliably");
    expect(draft.update).not.toHaveBeenCalled();
  });
  it("shows measured input level and clears it while paused", async () => {
    render(view());
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByRole("button", { name: "Done" });
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByRole("meter")).not.toBeInTheDocument();
    act(() => mocks.meter.notify(0.65));
    const meter = screen.getByRole("meter");
    expect(meter).toHaveAttribute("aria-valuenow", "65");
    expect(screen.getByText("65%")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Pause dictation" }));
    expect(meter).toHaveAttribute("aria-valuenow", "0");
    expect(screen.getByText("0%")).toBeInTheDocument();
  });

  it("one click starts live dictation, Done keeps final text in the draft", async () => {
    render(view());
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByText("Listening");
    expect(getUserMedia).toHaveBeenCalledOnce();
    act(() => partial("live text"));
    expect(draft.update).toHaveBeenCalledWith("live text");
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.getByText("Finishing transcription...")).toBeInTheDocument();
    await act(async () => resolve("Final text"));
    expect(draft.update).toHaveBeenLastCalledWith("Final text");
    expect(draft.finish).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Start dictation" })).not.toHaveFocus();
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

  it("offers retry of a failed batch without recording again", async () => {
    mocks.local = { adapter: null, status: null };
    mocks.mutateAsync.mockRejectedValueOnce(new Error("network failed")).mockResolvedValueOnce("Recovered text");
    render(view());
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByText("Recording · text appears after Done");
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    const retry = await screen.findByRole("button", { name: "Retry transcription" });
    const audio = mocks.mutateAsync.mock.calls[0]?.[0].recording;
    fireEvent.click(retry);
    await waitFor(() => expect(draft.finish).toHaveBeenCalledOnce());
    expect(getUserMedia).toHaveBeenCalledOnce();
    expect(mocks.mutateAsync.mock.calls[1]?.[0].recording).toBe(audio);
    expect(draft.update).toHaveBeenLastCalledWith("Recovered text");
  });

  it("starts and finishes with the composer shortcut and ignores key repeats", async () => {
    render(view());
    const microphone = screen.getByRole("button", { name: "Start dictation" });
    fireEvent.keyDown(microphone, { key: "M", ctrlKey: true, shiftKey: true });
    await screen.findByText("Listening");
    const done = screen.getByRole("button", { name: "Done" });
    fireEvent.keyDown(done, { key: "M", ctrlKey: true, shiftKey: true, repeat: true });
    expect(streamStop).not.toHaveBeenCalled();
    fireEvent.keyDown(done, { key: "M", ctrlKey: true, shiftKey: true });
    expect(screen.getByText("Finishing transcription...")).toBeInTheDocument();
    await act(async () => resolve("Shortcut draft"));
    expect(draft.finish).toHaveBeenCalledOnce();
  });

  it.each(["outside", "handled"])("does not cancel dictation for an %s Escape event", async (target) => {
    render(<><div data-chat-composer>{view()}</div><input aria-label="Other input" /></>);
    fireEvent.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByText("Listening");
    const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    if (target === "handled") event.preventDefault();
    fireEvent(target === "outside" ? screen.getByLabelText("Other input") : screen.getByRole("button", { name: "Done" }), event);
    expect(draft.cancel).not.toHaveBeenCalled();
    expect(screen.getByText("Listening")).toBeInTheDocument();
  });
});
