"use client";

import { useMemo } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { CreateDocumentComment, DocumentComment } from "../api/document-comments";
import type { DocumentRef } from "./doc-mode-store";
import { documentCommentScopeKey } from "./document-comment-identity";

const documentCommentKey = (workspaceId: string, ref: DocumentRef) => [
  "document-comments", workspaceId, documentCommentScopeKey(workspaceId, ref),
] as const;

export function useDocumentComments(workspaceId: string, ref: DocumentRef, enabled: boolean) {
  const query = useInfiniteQuery({
    queryKey: documentCommentKey(workspaceId, ref),
    queryFn: ({ pageParam, signal }) => api.documentComments.list(workspaceId, ref, pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: (page) => page.has_more ? page.next_offset : undefined,
    enabled: enabled && !!workspaceId && !!ref.id,
    staleTime: 15_000,
    refetchInterval: enabled ? 15_000 : false,
    retry: false,
  });
  const comments = useMemo(() => [...new Map((query.data?.pages ?? []).flatMap((page) => page.comments).map((comment) => [comment.id, comment])).values()], [query.data]);
  return { ...query, comments };
}

export function useCreateDocumentComment(workspaceId: string, ref: DocumentRef) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: Omit<CreateDocumentComment, "resource">) => api.documentComments.create(workspaceId, { ...input, resource: ref }),
    onSettled: () => client.invalidateQueries({ queryKey: documentCommentKey(workspaceId, ref) }),
  });
}

export function useResolveDocumentComment(workspaceId: string, ref: DocumentRef) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ comment, resolved }: { comment: DocumentComment; resolved: boolean }) => api.documentComments.resolve(workspaceId, { resource: ref, comment, resolved }),
    onSettled: () => client.invalidateQueries({ queryKey: documentCommentKey(workspaceId, ref) }),
  });
}
