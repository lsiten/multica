"use client";

import { useEffect, useRef, useState } from "react";
import type { VscreenLocalOperation, VscreenLocalResult, VscreenScope } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";

// Candidate titles stay in component memory; never place this payload in Query or a store.
export function MirrorWindowSelection({ scope, interventionId, disabled, control, onAdopted }: {
  readonly scope: VscreenScope;
  readonly interventionId: string;
  readonly disabled: boolean;
  readonly control: (scope: VscreenScope, operation: VscreenLocalOperation) => Promise<VscreenLocalResult>;
  readonly onAdopted: () => Promise<unknown>;
}) {
  const { t } = useT("runtimes");
  const [candidates, setCandidates] = useState<VscreenLocalResult["candidates"]>();
  const [selected, setSelected] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const generation = useRef(0);
  useEffect(() => () => { generation.current++; }, []);
  useEffect(() => {
    if (!candidates) return;
    const timeout = setTimeout(() => {
      setCandidates(undefined);
      setSelected("");
      setError("selection_expired");
    }, 10_000);
    return () => clearTimeout(timeout);
  }, [candidates]);

  async function execute(adopt: boolean) {
    const request = ++generation.current;
    setPending(true);
    setError(undefined);
    if (!adopt) { setCandidates(undefined); setSelected(""); }
    try {
      const result = await control(scope, adopt ? { action: "adopt_window", interventionId, windowHandle: selected } : { action: "list_windows", interventionId });
      if (request !== generation.current) return;
      if (!result.ok) throw new Error(result.reason ?? "handoff_failed");
      if (adopt) {
        setCandidates(undefined);
        setSelected("");
        await onAdopted();
      } else {
        if (!result.candidates) throw new Error("handoff_failed");
        setCandidates(result.candidates);
      }
    } catch (cause) {
      if (request !== generation.current) return;
      setCandidates(undefined);
      setSelected("");
      setError(cause instanceof Error ? cause.message : "handoff_failed");
    } finally {
      if (request === generation.current) setPending(false);
    }
  }

  return <div className="space-y-2">
    <p className="text-caption text-muted-foreground">{t(($) => $.vscreen.handoff.selection_help)}</p>
    <Button size="sm" variant="outline" disabled={disabled || pending} aria-busy={pending} onClick={() => void execute(false)}>{t(($) => $.vscreen.handoff.list_windows)}</Button>
    {candidates && <>
      {candidates.windows.length ? <label className="block text-caption">{t(($) => $.vscreen.handoff.window)}
        <select className="mt-1 block max-w-full rounded border bg-background p-1" value={selected} disabled={disabled || pending} onChange={(event) => setSelected(event.target.value)}>
          <option value="">{t(($) => $.vscreen.handoff.choose_window)}</option>
          {candidates.windows.map((window) => <option key={window.handle} value={window.handle}>{window.title ? `${window.title} · ${window.bundleId}` : window.bundleId}</option>)}
        </select>
      </label> : <p role="status" className="text-caption">{t(($) => $.vscreen.handoff.no_windows)}</p>}
      {candidates.truncated && <p className="text-caption text-muted-foreground">{t(($) => $.vscreen.handoff.windows_truncated)}</p>}
      <Button size="sm" disabled={disabled || pending || !candidates.windows.some((window) => window.handle === selected)} aria-busy={pending} onClick={() => void execute(true)}>{t(($) => $.vscreen.handoff.adopt_window)}</Button>
    </>}
    {error && <p role="alert" className="text-caption text-muted-foreground">{error === "selection_expired" || error === "stale_intervention" || error === "selection_unavailable" ? t(($) => $.vscreen.handoff.selection_expired) : error === "report_pending" ? t(($) => $.vscreen.handoff.report_pending) : error === "accessibility_denied" ? t(($) => $.vscreen.handoff.ax_required) : t(($) => $.vscreen.handoff.failed)}</p>}
  </div>;
}
