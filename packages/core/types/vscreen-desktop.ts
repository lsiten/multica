import { z } from "zod";
import type { VscreenScope } from "./vscreen";
export const vscreenDesktopActionSchema = z.discriminatedUnion("action", [
  z.object({ action: z.literal("status") }),
  z.object({ action: z.literal("list_windows"), interventionId: z.string().min(1).max(128) }),
  z.object({ action: z.literal("adopt_window"), interventionId: z.string().min(1).max(128), windowHandle: z.string().min(1).max(128) }),
  z.object({ action: z.literal("takeover"), interventionId: z.string().min(1).max(128), destinationSourceId: z.string().min(1).max(512) }),
  z.object({ action: z.literal("return"), interventionId: z.string().min(1).max(128), summary: z.string().refine((s) => new TextEncoder().encode(s).length <= 2048) }),
  z.object({ action: z.literal("settings"), permission: z.enum(["accessibility", "screenRecording"]) }),
]);
export type VscreenDesktopAction = z.infer<typeof vscreenDesktopActionSchema>;
export type VscreenDesktopResult = { ok: boolean; reason?: string; local: boolean; interventionId?: string; selectionRequired?: boolean; candidates?: { windows: { handle: string; bundleId: string; title: string }[]; truncated: boolean } };
export type VscreenDesktopRequest = { scope: VscreenScope; operation: VscreenDesktopAction };

export const credentialSchema = z.object({ capability: z.string().regex(/^[a-f0-9]{64}$/), incarnation: z.string().regex(/^[a-f0-9]{32}$/), pid: z.number().int().positive(), profile: z.string(), daemon_id: z.string().min(1), backend: z.string(), started_at: z.string().datetime({ offset: true }) });
export const healthSchema = z.object({ status: z.literal("running"), pid: z.number().int().positive(), profile: z.string(), daemon_id: z.string(), server_url: z.string(), launched_by: z.literal("desktop"), os: z.literal("darwin"), workspaces: z.array(z.object({ id: z.string(), runtimes: z.array(z.string()) })) });
export const resultSchema = z.object({
  ok: z.boolean(), local: z.boolean(), reason: z.string().optional(),
  intervention_id: z.string().max(128).optional(), selection_required: z.boolean().optional(),
  candidates: z.object({ windows: z.array(z.object({ handle: z.string().min(1).max(128), bundle_id: z.string().min(1).max(255), title: z.string().max(256) }).strict()).max(64), truncated: z.boolean() }).optional(),
}).transform((wire) => ({ ok: wire.ok, local: wire.local, reason: wire.reason, interventionId: wire.intervention_id, selectionRequired: wire.selection_required,
  candidates: wire.candidates ? { windows: wire.candidates.windows.map((window) => ({ handle: window.handle, bundleId: window.bundle_id, title: window.title })), truncated: wire.candidates.truncated } : undefined,
}));
