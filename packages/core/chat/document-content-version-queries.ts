"use client";

import {
  useMutation,
  useQuery,
  useQueryClient,
  queryOptions,
} from "@tanstack/react-query";
import { api, ApiError } from "../api";
import {
  type DocumentContentVersion,
  type DocumentContentVersionList,
  type RecordDocumentContentVersionRequest,
} from "../api/document-content-version";
import type { DocumentRef } from "./doc-mode-store";

// Query/mutation hooks for a document's content versions — the "based-on-document
// conversation + editing" surface. A document is either an issue (its body is
// issue.description, text) or an attachment (a binary blob, versioned but never
// edited in place). These hooks are the TanStack Query data layer that the
// doc-mode UI reads; they sit on top of the client's content-versions methods
// (already schema-parsed via parseWithFallback).

// Workspace-scoped keys so a version change invalidates exactly this document's
// projections.
export const documentContentVersionKeys = {
  all: (wsId: string) => ["document-content-versions", wsId] as const,
  current: (wsId: string, ref: DocumentRef) =>
    [...documentContentVersionKeys.all(wsId), ref.kind, ref.id, "current"] as const,
  list: (wsId: string, ref: DocumentRef) =>
    [...documentContentVersionKeys.all(wsId), ref.kind, ref.id, "list"] as const,
  single: (wsId: string, ref: DocumentRef, revision: number) =>
    [...documentContentVersionKeys.all(wsId), ref.kind, ref.id, revision] as const,
};

// The current (latest) version — the body the doc-mode view renders by default.
// A 404 (no history yet) is a real "no version" state, so it surfaces as a
// rejection rather than a cached null the UI would treat as "empty content".
export function documentContentVersionCurrentOptions(
  wsId: string,
  ref: DocumentRef,
) {
  return queryOptions({
    queryKey: documentContentVersionKeys.current(wsId, ref),
    queryFn: ({ signal }) =>
      ref.kind === "attachment"
        ? api.getAttachmentContentVersionCurrent(ref.id, signal)
        : api.getIssueContentVersionCurrent(ref.id, signal),
    enabled: !!ref.id && !!wsId && ref.kind !== "workspace_file",
    // A document body is stable between edits; the record mutation invalidates
    // it, so a long staleTime keeps the doc-mode view cheap and offline-tolerant.
    staleTime: 30_000,
    retry: (failureCount, error) => {
      // A 404 (no version yet) is not a transient failure — don't spin.
      if (error instanceof ApiError && error.status === 404) return false;
      return failureCount < 2;
    },
  });
}

export function useDocumentContentVersionCurrent(wsId: string, ref: DocumentRef) {
  return useQuery(documentContentVersionCurrentOptions(wsId, ref));
}

// The full version history (revision DESC) — powers the diff view (D6) and the
// rollback controls. Absent for a document with no history yet.
export function documentContentVersionsOptions(
  wsId: string,
  ref: DocumentRef,
) {
  return queryOptions({
    queryKey: documentContentVersionKeys.list(wsId, ref),
    queryFn: ({ signal }) =>
      ref.kind === "attachment"
        ? api.listAttachmentContentVersions(ref.id, signal)
        : api.listIssueContentVersions(ref.id, signal),
    enabled: !!ref.id && !!wsId && ref.kind !== "workspace_file",
    staleTime: 10_000,
  });
}

export function useDocumentContentVersions(wsId: string, ref: DocumentRef) {
  return useQuery(documentContentVersionsOptions(wsId, ref));
}

// A single historical version — for the diff view (two versions diffed on the
// client, D6) and per-version rollback.
export function documentContentVersionOptions(
  wsId: string,
  ref: DocumentRef,
  revision: number,
) {
  return queryOptions({
    queryKey: documentContentVersionKeys.single(wsId, ref, revision),
    queryFn: ({ signal }) =>
      ref.kind === "attachment"
        ? api.getAttachmentContentVersion(ref.id, revision, signal)
        : api.getIssueContentVersion(ref.id, revision, signal),
    enabled: !!ref.id && !!wsId && revision > 0 && ref.kind !== "workspace_file",
  });
}

export function useDocumentContentVersion(wsId: string, ref: DocumentRef, revision: number) {
  return useQuery(documentContentVersionOptions(wsId, ref, revision));
}

// Commit a new version (or merge into the current one, D4). On success the
// current + list keys are refetched so the doc-mode view and version list update
// in place; the conversation is untouched (D8).
export function useRecordDocumentContentVersion(wsId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({
      ref,
      input,
    }: {
      ref: DocumentRef;
      input: RecordDocumentContentVersionRequest;
    }): Promise<DocumentContentVersion> => {
      return ref.kind === "attachment"
        ? api.recordAttachmentContentVersion(ref.id, input)
        : ref.kind === "issue"
          ? api.recordIssueContentVersion(ref.id, input)
          : Promise.reject(new Error("workspace files are read-only"));
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: documentContentVersionKeys.all(wsId) });
    },
  });
}

// Restore a target version — the server appends it as a NEW version (linear
// history), so the same invalidation refreshes both the body and the list.
export function useRollbackDocumentContentVersion(wsId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({
      ref,
      revision,
      input,
    }: {
      ref: DocumentRef;
      revision: number;
      input?: RecordDocumentContentVersionRequest;
    }): Promise<DocumentContentVersion> => {
      return ref.kind === "attachment"
        ? api.rollbackAttachmentContentVersion(ref.id, revision, input)
        : ref.kind === "issue"
          ? api.rollbackIssueContentVersion(ref.id, revision, input)
          : Promise.reject(new Error("workspace files are read-only"));
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: documentContentVersionKeys.all(wsId) });
    },
  });
}

export type { DocumentContentVersion, DocumentContentVersionList };
