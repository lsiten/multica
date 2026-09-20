import type { SourcePoint } from "./mirror-coordinates";
import type { MirrorControlInput, MirrorControlPointer } from "./video-session";

export interface MirrorPointerSample {
  readonly id: number;
  readonly type: string;
  readonly clientX: number;
  readonly clientY: number;
  readonly point: SourcePoint;
  readonly button: MirrorControlPointer["button"];
}

/** Owns a single captured gesture; continuous input is bounded to one frame. */
export class MirrorGestures {
  private gesture: {
    id: string;
    start: MirrorPointerSample;
    last: MirrorPointerSample;
    mode: "pending" | "drag" | "scroll";
  } | null = null;
  private hold: ReturnType<typeof setTimeout> | null = null;
  private frame: number | null = null;
  private pending: MirrorControlInput | null = null;

  constructor(private readonly send: (input: MirrorControlInput) => void) {}

  down(sample: MirrorPointerSample): boolean {
    if (this.gesture) return false;
    this.flush();
    const gesture = {
      id: crypto.randomUUID(), start: sample, last: sample,
      mode: sample.type === "touch" ? "pending" as const : "drag" as const,
    };
    this.gesture = gesture;
    if (gesture.mode === "drag") this.pointer("pointer:down", sample);
    else this.hold = setTimeout(() => {
      this.hold = null;
      if (this.gesture !== gesture) return;
      this.gesture.mode = "drag";
      this.pointer("pointer:down", gesture.last);
    }, 350);
    return true;
  }

  move(sample: MirrorPointerSample): void {
    const gesture = this.gesture;
    if (!gesture || gesture.start.id !== sample.id) return;
    const previous = gesture.last;
    if (gesture.mode === "pending") {
      if (Math.hypot(sample.clientX - gesture.start.clientX, sample.clientY - gesture.start.clientY) < 8) return;
      this.clearHold();
      gesture.mode = "scroll";
    }
    gesture.last = sample;
    switch (gesture.mode) {
      case "scroll":
        this.wheel({ ...gesture.start.point, deltaX: previous.point.x - sample.point.x, deltaY: previous.point.y - sample.point.y });
        break;
      case "drag":
        this.pending = { kind: "pointer:move", gestureId: gesture.id, pointer: { ...sample.point, button: gesture.start.button } };
        this.schedule();
        break;
    }
  }

  up(sample: MirrorPointerSample): void {
    const gesture = this.gesture;
    if (!gesture || gesture.start.id !== sample.id) return;
    this.clearHold();
    this.flush();
    switch (gesture.mode) {
      case "pending":
        this.pointer("pointer:down", gesture.start);
        this.pointer("pointer:up", gesture.start);
        break;
      case "drag":
        this.pointer("pointer:up", sample);
        break;
      case "scroll":
        break;
    }
    this.gesture = null;
  }

  wheel(pointer: MirrorControlPointer): void {
    if (this.pending && this.pending.kind !== "wheel") this.flush();
    const previous = this.pending?.pointer;
    this.pending = {
      kind: "wheel", gestureId: this.pending?.gestureId ?? crypto.randomUUID(),
      pointer: { ...pointer, deltaX: (previous?.deltaX ?? 0) + (pointer.deltaX ?? 0), deltaY: (previous?.deltaY ?? 0) + (pointer.deltaY ?? 0) },
    };
    this.schedule();
  }

  cancel(): void {
    this.clearHold();
    if (this.frame !== null) cancelAnimationFrame(this.frame);
    this.frame = null;
    this.pending = null;
    if (this.gesture?.mode === "drag") this.pointer("pointer:up", this.gesture.last);
    this.gesture = null;
  }

  owns(id: number): boolean { return this.gesture?.start.id === id; }

  private pointer(kind: "pointer:down" | "pointer:up", sample: MirrorPointerSample): void {
    if (this.gesture) this.send({ kind, gestureId: this.gesture.id, pointer: { ...sample.point, button: this.gesture.start.button } });
  }

  private clearHold(): void {
    if (this.hold !== null) clearTimeout(this.hold);
    this.hold = null;
  }

  private schedule(): void {
    if (this.frame === null) this.frame = requestAnimationFrame(() => this.flush());
  }

  private flush(): void {
    if (this.frame !== null) cancelAnimationFrame(this.frame);
    this.frame = null;
    const input = this.pending;
    this.pending = null;
    if (input) this.send(input);
  }
}
