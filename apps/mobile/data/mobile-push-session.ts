import type { MobilePushConfiguration, MobilePushDevice } from "./mobile-push-schema";

export type PushStatus = "checking" | "unsupported" | "build_required" | "server_unavailable" | "permission_required" | "permission_denied" | "registered" | "error";
export interface PushNative {
  availability(): Promise<Omit<MobilePushDevice, "token"> | "unsupported" | "build_required">;
  permission(request: boolean): Promise<"granted" | "undetermined" | "denied">;
  token(): Promise<string>;
  unregister(): Promise<void>;
}
interface PushDependencies {
  readonly native: PushNative;
  readonly configuration: () => Promise<MobilePushConfiguration>;
  readonly register: (device: MobilePushDevice, signal: AbortSignal) => Promise<void>;
  readonly remove: (device: MobilePushDevice) => Promise<void>;
  readonly onStatus: (status: PushStatus) => void;
}

export class MobilePushSession {
  private userId: string | null = null;
  private revision = 0;
  private controller = new AbortController();
  private device: MobilePushDevice | null = null;
  private nativeCleanup: Promise<void> = Promise.resolve();
  private refreshing: Promise<void> | null = null;

  constructor(private readonly dependencies: PushDependencies) {}

  setUser(userId: string | null) {
    if (userId === this.userId) return;
    this.revision++;
    this.controller.abort();
    this.controller = new AbortController();
    this.refreshing = null;
    const previousUser = this.userId;
    this.userId = userId;
    const device = this.device;
    this.device = null;
    // Capture the outgoing bearer credential before the auth store clears it.
    if (previousUser && device) {
      void this.dependencies.remove(device).catch(() => {
        console.warn("[push] Device removal could not reach the server");
      });
    }
    if (previousUser) {
      this.nativeCleanup = this.nativeCleanup.then(() => this.dependencies.native.unregister()).catch(() => {
        console.warn("[push] Native notification cleanup failed");
      });
    }
    if (userId) void this.refresh();
  }

  refresh(requestPermission = false): Promise<void> {
    if (!this.userId) return Promise.resolve();
    if (this.refreshing) return this.refreshing;
    const revision = this.revision;
    const signal = this.controller.signal;
    const current = () => revision === this.revision;
    const run = async () => {
      this.dependencies.onStatus("checking");
      try {
        await this.nativeCleanup;
        if (!current()) return;
        const availability = await this.dependencies.native.availability();
        if (!current()) return;
        if (typeof availability === "string") {
          this.dependencies.onStatus(availability);
          return;
        }
        const config = await this.dependencies.configuration();
        if (!current()) return;
        if (!config.enabled || !config.topics.includes(availability.topic) || !config.environments.includes(availability.environment)) {
          this.dependencies.onStatus("server_unavailable");
          return;
        }
        const permission = await this.dependencies.native.permission(requestPermission);
        if (!current()) return;
        if (permission !== "granted") {
          this.dependencies.onStatus(permission === "denied" ? "permission_denied" : "permission_required");
          return;
        }
        const token = await this.dependencies.native.token();
        if (!current()) return;
        this.device = { ...availability, token };
        await this.dependencies.register(this.device, signal);
        if (current()) this.dependencies.onStatus("registered");
      } catch (error) {
        if (current() && !signal.aborted) {
          console.warn("[push] Device registration failed", error instanceof Error ? error.name : "unknown");
          this.dependencies.onStatus("error");
        }
      }
    };
    const operation = run().finally(() => {
      if (revision === this.revision) this.refreshing = null;
    });
    this.refreshing = operation;
    return operation;
  }
}
