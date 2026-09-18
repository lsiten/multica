/** Cancelable gathering for a viewer that may close or change source during negotiation. */
export function gatherVideoIce(
  peer: RTCPeerConnection,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve, reject) => {
    let grace: ReturnType<typeof setTimeout> | undefined;
    const cleanup = () => {
      clearTimeout(deadline);
      clearTimeout(grace);
      peer.removeEventListener("icegatheringstatechange", changed);
      peer.removeEventListener("icecandidate", candidate);
      signal.removeEventListener("abort", aborted);
    };
    const finish = () => {
      cleanup();
      resolve();
    };
    const aborted = () => {
      cleanup();
      reject(signal.reason);
    };
    const changed = () => {
      if (peer.iceGatheringState === "complete") finish();
    };
    const candidate = (event: RTCPeerConnectionIceEvent) => {
      if (event.candidate?.type === "relay" && !grace)
        grace = setTimeout(finish, 1_000);
    };
    const deadline = setTimeout(() => {
      cleanup();
      reject(new DOMException("ICE gathering timed out", "TimeoutError"));
    }, 20_000);
    peer.addEventListener("icegatheringstatechange", changed);
    peer.addEventListener("icecandidate", candidate);
    signal.addEventListener("abort", aborted, { once: true });
    if (signal.aborted) aborted();
    else changed();
  });
}
