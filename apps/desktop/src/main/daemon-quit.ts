export interface DaemonQuitEvent {
  preventDefault(): void;
}

export function createDaemonQuitHandler(options: {
  prepare: () => void;
  autoStop: () => Promise<boolean>;
  stop: () => Promise<unknown>;
  quit: () => void;
  timeoutMs?: number;
}): (event: DaemonQuitEvent) => void {
  let quitting = false;
  let released = false;
  return (event) => {
    if (released) return;
    event.preventDefault();
    if (quitting) return;
    quitting = true;
    options.prepare();
    const finish = () => {
      if (released) return;
      released = true;
      clearTimeout(deadline);
      options.quit();
    };
    const deadline = setTimeout(finish, options.timeoutMs ?? 15_000);
    void (async () => {
      try {
        if (await options.autoStop()) await options.stop();
      } finally {
        finish();
      }
    })().catch(() => { console.warn("Local runtime shutdown did not complete before desktop exit"); });
  };
}
