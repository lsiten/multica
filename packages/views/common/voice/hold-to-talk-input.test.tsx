import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "../../test/i18n";
import { HoldToTalkInput } from "./hold-to-talk-input";

const stopTrack = vi.fn();
const stream = { getTracks: () => [{ stop: stopTrack }] };
const getUserMedia = vi.fn(async () => stream);
const onVoice = vi.fn(async (_blob: Blob) => undefined);
const onTranscript = vi.fn();
const recorders: FakeRecorder[] = [];
class FakeRecorder {
  state = "inactive";
  mimeType = "audio/webm";
  ondataavailable?: (event: { data: Blob }) => void;
  onstop?: () => void;
  onerror?: () => void;
  constructor() { recorders.push(this); }
  start() { this.state = "recording"; }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob(["recorded audio"]) });
    this.onstop?.();
  }
}

function view(transcript = "", enabled = true) {
  return <I18nProvider locale="en" resources={RESOURCES}>
    <HoldToTalkInput enabled={enabled} transcript={transcript} onVoice={onVoice} onTranscript={onTranscript} />
  </I18nProvider>;
}
async function hold() {
  const button = screen.getByRole("button", { name: "Hold to talk" });
  fireEvent.keyDown(button, { key: " " });
  await screen.findByText("Release to transcribe");
  return button;
}

beforeEach(() => {
  vi.clearAllMocks();
  recorders.length = 0;
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
  vi.stubGlobal("PointerEvent", class extends MouseEvent {
    pointerId: number;
    constructor(type: string, options: PointerEventInit) {
      super(type, options);
      this.pointerId = options.pointerId ?? 1;
    }
  });
});
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("HoldToTalkInput", () => {
  it("accepts a direct transcription response without a mirror session", async () => {
    const transcribe = vi.fn(async () => "Direct transcript");
    render(<I18nProvider locale="en" resources={RESOURCES}>
      <HoldToTalkInput enabled onVoice={transcribe} onTranscript={onTranscript} />
    </I18nProvider>);
    fireEvent.keyUp(await hold(), { key: " " });
    await waitFor(() => expect(onTranscript).toHaveBeenCalledWith("Direct transcript"));
  });

  it("aborts pending transcription and ignores a late response after unmount", async () => {
    let finish!: (text: string) => void;
    let signal: AbortSignal | undefined;
    const transcribe = vi.fn((_blob: Blob, requestSignal: AbortSignal) => {
      signal = requestSignal;
      return new Promise<string>((resolve) => { finish = resolve; });
    });
    const { unmount } = render(<I18nProvider locale="en" resources={RESOURCES}>
      <HoldToTalkInput enabled onVoice={transcribe} onTranscript={onTranscript} />
    </I18nProvider>);
    fireEvent.keyUp(await hold(), { key: " " });
    await waitFor(() => expect(transcribe).toHaveBeenCalledOnce());
    unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => finish("Late transcript"));
    expect(onTranscript).not.toHaveBeenCalled();
  });

  it.each([false, true])("handles pointer release with slide cancellation %s", async (cancel) => {
    render(view());
    const button = screen.getByRole("button", { name: "Hold to talk" });
    button.setPointerCapture = vi.fn();
    fireEvent.pointerDown(button, { button: 0, pointerId: 1 });
    await screen.findByText("Release to transcribe");
    if (cancel) {
      fireEvent.pointerMove(button, { pointerId: 1, clientY: -80 });
      expect(screen.getByText("Release to cancel")).toBeInTheDocument();
    }
    fireEvent.pointerUp(button, { pointerId: 1 });
    await act(async () => {});
    expect(onVoice).toHaveBeenCalledTimes(cancel ? 0 : 1);
    expect(stopTrack).toHaveBeenCalled();
  });

  it("records only while held and returns transcription for editing on release", async () => {
    const { rerender } = render(view());
    const button = await hold();
    expect(onVoice).not.toHaveBeenCalled();
    fireEvent.keyUp(button, { key: " " });
    await waitFor(() => expect(onVoice).toHaveBeenCalledOnce());
    expect(stopTrack).toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Transcribing…" })).toBeDisabled();
    expect(onTranscript).not.toHaveBeenCalled();
    rerender(view("Recognized words"));
    await waitFor(() => expect(onTranscript).toHaveBeenCalledWith("Recognized words"));
    expect(screen.getByRole("button", { name: "Hold to talk" })).toBeEnabled();
  });

  it("discards a late microphone grant after release", async () => {
    let grant!: (value: typeof stream) => void;
    getUserMedia.mockImplementationOnce(() => new Promise((resolve) => { grant = resolve; }));
    render(view());
    const button = screen.getByRole("button", { name: "Hold to talk" });
    fireEvent.keyDown(button, { key: " " });
    fireEvent.keyUp(button, { key: " " });
    await act(async () => grant(stream));
    expect(stopTrack).toHaveBeenCalledOnce();
    expect(recorders).toHaveLength(0);
    expect(onVoice).not.toHaveBeenCalled();
  });

  it.each(["escape", "blur", "unmount", "disabled"])("cancels recording on %s without uploading audio", async (reason) => {
    const { unmount, rerender } = render(view());
    await hold();
    if (reason === "escape") fireEvent.keyDown(window, { key: "Escape" });
    if (reason === "blur") fireEvent.blur(window);
    if (reason === "unmount") unmount();
    if (reason === "disabled") rerender(view("", false));
    expect(stopTrack).toHaveBeenCalled();
    expect(recorders[0]?.state).toBe("inactive");
    expect(onVoice).not.toHaveBeenCalled();
  });

  it("shows an actionable microphone error", async () => {
    getUserMedia.mockRejectedValueOnce(new Error("denied"));
    render(view());
    fireEvent.keyDown(screen.getByRole("button"), { key: " " });
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not record or transcribe");
  });

  it("handles a failed audio upload", async () => {
    onVoice.mockRejectedValueOnce(new Error("disconnected"));
    render(view());
    fireEvent.keyUp(await hold(), { key: " " });
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not record or transcribe");
  });

  it("ends the pending state when no transcript arrives", async () => {
    render(view());
    const button = await hold();
    vi.useFakeTimers();
    fireEvent.keyUp(button, { key: " " });
    await act(async () => vi.advanceTimersByTimeAsync(30_000));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Hold to talk" })).toBeEnabled();
  });
});
