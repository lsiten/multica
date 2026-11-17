import { z } from "zod";
import type { DocumentRef } from "./doc-mode-store";

export const DocumentResourceSchema = z.object({
  kind: z.enum(["issue", "attachment", "workspace_file"]),
  id: z.string(),
  label: z.string().optional(),
  contentType: z.string().optional(),
  sessionId: z.string().optional(),
  projectId: z.string().optional(),
  taskId: z.string().optional(),
  projectResourceId: z.string().optional(),
  runtimeId: z.string().optional(),
  workDir: z.string().optional(),
  relativePath: z.string().optional(),
  fileId: z.string().uuid().optional(),
  directoryId: z.string().regex(/^[a-f0-9]{64}$/).optional(),
  directoryRevision: z.number().int().nonnegative().safe().optional(),
});

const MessageSourceSchema = z.object({
  workspaceId: z.string().min(1), sessionId: z.string().min(1), messageId: z.string().min(1),
  role: z.enum(["user", "assistant"]), createdAt: z.string(),
  authorId: z.string().optional(), attachmentIds: z.array(z.string()).optional(),
});

export const ChatReferenceSchema = z.object({
  id: z.string(),
  label: z.string(),
  content: z.string(),
  source: z.object({
    workspaceId: z.string(),
    resource: DocumentResourceSchema,
    revision: z.number().int().positive().optional(),
    state: z.enum(["saved", "draft"]),
    extent: z.enum(["document", "selection"]),
  }).optional(),
  messageSource: MessageSourceSchema.optional(),
});

export type ChatReference = z.infer<typeof ChatReferenceSchema>;
const SentDocumentReferenceSchema = z.object({ label: z.string(), ...ChatReferenceSchema.shape.source.unwrap().shape });
export type SentDocumentReference = z.infer<typeof SentDocumentReferenceSchema>;
const SentMessageReferenceSchema = MessageSourceSchema.extend({ label: z.string() });
export type SentMessageReference = z.infer<typeof SentMessageReferenceSchema>;

function readMetadata<T>(content: string, marker: "document" | "message", schema: z.ZodType<T>): T[] {
  const references: T[] = [];
  let fence: { readonly character: string; readonly length: number } | undefined;
  for (const line of content.split("\n")) {
    const delimiter = /^ {0,3}(`{3,}|~{3,})/.exec(line)?.[1];
    if (delimiter) {
      if (!fence) fence = { character: delimiter[0] ?? "", length: delimiter.length };
      else if (delimiter.startsWith(fence.character) && delimiter.length >= fence.length && line.trim() === delimiter) fence = undefined;
      continue;
    }
    if (fence) continue;
    const prefix = `<!-- multica-${marker}-reference:`;
    if (!line.startsWith(prefix) || !line.endsWith(" -->")) continue;
    try {
      const parsed = schema.safeParse(JSON.parse(line.slice(prefix.length, -4)));
      if (parsed.success) references.push(parsed.data);
    } catch (error) {
      if (!(error instanceof SyntaxError)) throw error;
    }
  }
  return references;
}

export function readSentDocumentReferences(content: string): SentDocumentReference[] {
  return readMetadata(content, "document", SentDocumentReferenceSchema);
}

export function readSentMessageReferences(content: string): SentMessageReference[] {
  return readMetadata(content, "message", SentMessageReferenceSchema);
}
export interface DocumentReferenceInput {
  readonly resource: DocumentRef;
  readonly content: string;
  readonly revision?: number;
  readonly state: "saved" | "draft";
  readonly extent: "document" | "selection";
}

/** Snapshots stay readable and cannot terminate their own Markdown fence. */
export function composeChatReferences(content: string, references: readonly ChatReference[]): string {
  if (!references.length) return content;
  const quotes = references.map((reference) => {
    if (!reference.source && !reference.messageSource) return reference.content;
    const longestFence = Math.max(2, ...Array.from(reference.content.matchAll(/`+/g), (match) => match[0].length));
    const fence = "`".repeat(longestFence + 1);
    const metadata = JSON.stringify({ label: reference.label, ...(reference.source ?? reference.messageSource) }).replaceAll("<", "\\u003c").replaceAll(">", "\\u003e").replaceAll("mention://", "mention\\u003a//");
    const label = reference.label.replace(/[\\`*_[\]<>]/g, (character) => `&#${character.charCodeAt(0)};`).replaceAll(/\r?\n/g, " ").replaceAll("mention://", "mention&#58;//");
    const snapshot = reference.content.replaceAll("mention://", "mention&#58;//");
    const marker = reference.source ? "document" : "message";
    return [`<!-- multica-${marker}-reference:${metadata} -->`, `> ${label}${reference.source?.revision ? ` · v${reference.source.revision}` : ""}`, "", `${fence}text`, snapshot, fence].join("\n");
  }).join("\n\n---\n\n");
  const body = content.trim();
  const command = body.match(/^\/note(?=\s|$)/i)?.[0];
  return command
    ? `${command}\n\n${quotes}\n\n${body.slice(command.length).trim()}`.trim()
    : [quotes, body].filter(Boolean).join("\n\n");
}
