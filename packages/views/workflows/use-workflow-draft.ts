import { useCallback, useState, type SetStateAction } from "react";
import { sameDraft, type Draft } from "./workflow-draft";

export function useWorkflowDraft(source: Draft, target: string, version: string) {
  const sourceKey = JSON.stringify(source);
  const [state, setState] = useState(() => ({
    target, sourceKey, sourceVersion: version, version,
    baseline: source, draft: source, remoteChanged: false,
  }));
  const dirty = !sameDraft(state.draft, state.baseline);
  if (state.target !== target || state.sourceKey !== sourceKey || state.sourceVersion !== version) {
    if (state.target !== target || !dirty) {
      setState({ target, sourceKey, sourceVersion: version, version, baseline: source, draft: source, remoteChanged: false });
    } else {
      setState({ ...state, sourceKey, sourceVersion: version, remoteChanged: true });
    }
  }
  const setDraft = useCallback((next: SetStateAction<Draft>) => {
    setState((old) => ({ ...old, draft: typeof next === "function" ? next(old.draft) : next }));
  }, []);
  const commit = useCallback((next: Draft, nextVersion: string) => {
    setState((old) => ({ ...old, draft: next, baseline: next, version: nextVersion, remoteChanged: false }));
  }, []);
  const reload = useCallback(() => {
    setState({ target, sourceKey, sourceVersion: version, version, baseline: source, draft: source, remoteChanged: false });
  }, [source, sourceKey, target, version]);
  const conflict = useCallback(() => setState((old) => ({ ...old, remoteChanged: true })), []);

  return { draft: state.draft, setDraft, dirty, version: state.version, remoteChanged: state.remoteChanged, commit, reload, conflict };
}
