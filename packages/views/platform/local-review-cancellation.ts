import { createSafeId } from "@multica/core/utils";

export async function readWithCancellation(read: (id?: string) => Promise<unknown>, daemon: object, signal?: AbortSignal): Promise<unknown> {
  signal?.throwIfAborted();
  const id = signal ? createSafeId() : undefined;
  const cancel = "cancelLocalReviewRead" in daemon && typeof daemon.cancelLocalReviewRead === "function" ? daemon.cancelLocalReviewRead.bind(daemon) : undefined;
  const abort = () => { if (id) cancel?.(id); };
  signal?.addEventListener("abort", abort, { once: true });
  try {
    const response = await read(id);
    signal?.throwIfAborted();
    return response;
  } finally {
    signal?.removeEventListener("abort", abort);
  }
}
