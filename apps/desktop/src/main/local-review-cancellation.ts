import type { EventEmitter } from "node:events";

type Owner = Pick<EventEmitter, "once" | "removeListener">;
type PendingReads = {
  readonly requests: Map<string, AbortController>;
  readonly destroyed: () => void;
};

// Cancellation identifiers are scoped to the invoking WebContents, never shared
// across windows. Only read operations are registered here.
export class LocalReviewCancellation {
  private readonly owners = new WeakMap<Owner, PendingReads>();

  async run<T>(owner: Owner, id: string, read: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (!/^[a-zA-Z0-9_-]{1,128}$/.test(id)) throw new Error("Invalid review read identifier");
    let pending = this.owners.get(owner);
    if (!pending) {
      const requests = new Map<string, AbortController>();
      const destroyed = () => {
        for (const controller of requests.values()) controller.abort();
      };
      pending = { requests, destroyed };
      this.owners.set(owner, pending);
      owner.once("destroyed", destroyed);
    }
    if (pending.requests.has(id) || pending.requests.size >= 32) throw new Error("Review reads are busy");
    const controller = new AbortController();
    pending.requests.set(id, controller);
    try {
      return await read(controller.signal);
    } finally {
      pending.requests.delete(id);
      if (pending.requests.size === 0) {
        owner.removeListener("destroyed", pending.destroyed);
        this.owners.delete(owner);
      }
    }
  }

  cancel(owner: Owner, id: string): void {
    this.owners.get(owner)?.requests.get(id)?.abort();
  }
}
