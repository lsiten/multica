import { z } from "zod";
import type { VscreenScope } from "./vscreen";
export const vscreenDesktopActionSchema = z.discriminatedUnion("action", [
  z.object({ action: z.literal("status") }),
  z.object({ action: z.literal("takeover"), interventionId: z.string().min(1).max(128), destinationSourceId: z.string().min(1).max(512) }),
  z.object({ action: z.literal("return"), interventionId: z.string().min(1).max(128), summary: z.string().refine((s) => new TextEncoder().encode(s).length <= 2048) }),
  z.object({ action: z.literal("settings"), permission: z.enum(["accessibility", "screenRecording"]) }),
]);
export type VscreenDesktopAction = z.infer<typeof vscreenDesktopActionSchema>;
export type VscreenDesktopResult = { ok: boolean; reason?: string; local: boolean };
export type VscreenDesktopRequest = { scope: VscreenScope; operation: VscreenDesktopAction };

export const credentialSchema = z.object({ capability: z.string().regex(/^[a-f0-9]{64}$/), incarnation: z.string().regex(/^[a-f0-9]{32}$/), pid: z.number().int().positive(), profile: z.string(), daemon_id: z.string().min(1), backend: z.string(), started_at: z.string().datetime({ offset: true }) });
export const healthSchema = z.object({ status: z.literal("running"), pid: z.number().int().positive(), profile: z.string(), daemon_id: z.string(), server_url: z.string(), launched_by: z.literal("desktop"), os: z.literal("darwin"), workspaces: z.array(z.object({ id: z.string(), runtimes: z.array(z.string()) })) });
export const resultSchema = z.object({ ok: z.boolean(), local: z.boolean(), reason: z.string().optional() });
