"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";

export type DocumentSaveStatus =
  | "saved"
  | "dirty"
  | "saving"
  | "offline"
  | "conflict"
  | "error";

interface DocumentEditBase {
  readonly content: string;
  readonly revision: number;
  readonly contentType: string;
}

export function useDocumentAutosave({
  enabled,
  content,
  base,
  save,
  onBaseChange,
  onSaved,
  onConflict,
}: {
  readonly enabled: boolean;
  readonly content: string;
  readonly base: DocumentEditBase | null;
  readonly save: (
    input: DocumentEditBase & {
      readonly content: string;
      readonly merge: boolean;
      readonly note?: string;
    },
  ) => Promise<DocumentContentVersion>;
  readonly onBaseChange: (base: DocumentEditBase) => void;
  readonly onSaved: (base: DocumentEditBase) => void;
  readonly onConflict: () => void;
}) {
  const [status, setStatus] = useState<DocumentSaveStatus>("saved");
  const [lastSavedAt, setLastSavedAt] = useState<number | null>(null);
  const contentRef = useRef(content);
  const baseRef = useRef(base);
  const saveRef = useRef(save);
  const onBaseChangeRef = useRef(onBaseChange);
  const onSavedRef = useRef(onSaved);
  const onConflictRef = useRef(onConflict);
  const inFlight = useRef(false);
  const queuedSave = useRef<{
    readonly merge: boolean;
    readonly note?: string;
  } | null>(null);
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const maxTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const dirtyWasActive = useRef(false);
  const dirty = enabled && !!base && content !== base.content;

  useEffect(() => {
    contentRef.current = content;
    baseRef.current = base;
    saveRef.current = save;
    onBaseChangeRef.current = onBaseChange;
    onSavedRef.current = onSaved;
    onConflictRef.current = onConflict;
  }, [base, content, onBaseChange, onConflict, onSaved, save]);

  const clearTimers = useCallback(() => {
    if (idleTimer.current) clearTimeout(idleTimer.current);
    if (maxTimer.current) clearTimeout(maxTimer.current);
    idleTimer.current = null;
    maxTimer.current = null;
  }, []);

  const saveNow = useCallback(
    async (options: {
      readonly merge: boolean;
      readonly note?: string;
    } = { merge: true }) => {
      const snapshotBase = baseRef.current;
      if (!enabled || !snapshotBase) return null;
      if (typeof navigator !== "undefined" && !navigator.onLine) {
        setStatus("offline");
        return null;
      }
      if (inFlight.current) {
        queuedSave.current = {
          merge: queuedSave.current?.merge === false || !options.merge ? false : true,
          note: options.note ?? queuedSave.current?.note,
        };
        return null;
      }
      clearTimers();
      inFlight.current = true;
      setStatus("saving");
      const snapshot = {
        content: contentRef.current,
        revision: snapshotBase.revision,
        contentType: snapshotBase.contentType,
        merge: options.merge,
        note: options.note,
      };
      try {
        const version = await saveRef.current(snapshot);
        const nextBase = {
          content: snapshot.content,
          revision: version.owner_revision ?? version.revision,
          contentType: snapshot.contentType,
        };
        onBaseChangeRef.current(nextBase);
        if (contentRef.current === snapshot.content) {
          onSavedRef.current(nextBase);
          setLastSavedAt(Date.now());
          setStatus("saved");
        } else {
          setStatus("dirty");
        }
        return version;
      } catch (error) {
        if (
          error &&
          typeof error === "object" &&
          "status" in error &&
          error.status === 409
        ) {
          setStatus("conflict");
          onConflictRef.current();
        } else {
          setStatus(
            typeof navigator !== "undefined" && !navigator.onLine
              ? "offline"
              : "error",
          );
        }
        return null;
      } finally {
        inFlight.current = false;
        const queued = queuedSave.current;
        queuedSave.current = null;
        if (
          queued &&
          contentRef.current !== baseRef.current?.content
        ) {
          queueMicrotask(() => void saveNow(queued));
        }
      }
    },
    [clearTimers, enabled],
  );

  const rebase = useCallback(
    (nextBase: DocumentEditBase) => {
      baseRef.current = nextBase;
      dirtyWasActive.current = false;
      setStatus("dirty");
    },
    [],
  );

  useEffect(() => {
    if (!dirty) {
      if (dirtyWasActive.current) clearTimers();
      dirtyWasActive.current = false;
      return;
    }
    if (status === "saving" || status === "conflict") return;
    setStatus(navigator.onLine ? "dirty" : "offline");
    if (!dirtyWasActive.current) {
      dirtyWasActive.current = true;
      maxTimer.current = setTimeout(() => {
        maxTimer.current = null;
        void saveNow({ merge: true });
      }, 10_000);
    }
    if (idleTimer.current) clearTimeout(idleTimer.current);
    idleTimer.current = setTimeout(() => {
      idleTimer.current = null;
      void saveNow({ merge: true });
    }, 1_500);
    return () => {
      if (idleTimer.current) clearTimeout(idleTimer.current);
      idleTimer.current = null;
    };
  }, [base?.revision, clearTimers, content, dirty, saveNow, status]);

  useEffect(() => {
    const online = () => {
      if (baseRef.current && contentRef.current !== baseRef.current.content) {
        void saveNow({ merge: true });
      }
    };
    const offline = () => setStatus("offline");
    window.addEventListener("online", online);
    window.addEventListener("offline", offline);
    if (typeof navigator !== "undefined" && !navigator.onLine) {
      setStatus("offline");
    }
    return () => {
      window.removeEventListener("online", online);
      window.removeEventListener("offline", offline);
      clearTimers();
    };
  }, [clearTimers, saveNow]);

  return {
    status,
    dirty,
    lastSavedAt,
    saveNow: (merge = false, note?: string) => saveNow({ merge, note }),
    rebase,
    resetStatus: (next: DocumentSaveStatus = "saved") => setStatus(next),
  };
}
