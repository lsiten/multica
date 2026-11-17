// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { MobilePushSession, type PushNative, type PushStatus } from "./mobile-push-session";

function fixture() {
  const native: PushNative = {
    availability: vi.fn(async () => ({ topic: "test.app", environment: "sandbox" } as const)),
    permission: vi.fn(async () => "granted" as const),
    token: vi.fn(async () => "abcd"),
    unregister: vi.fn(async () => {}),
  };
  const states: PushStatus[] = [];
  const dependencies = {
    native,
    configuration: vi.fn(async () => ({ enabled: true, topics: ["test.app"], environments: ["sandbox"] })),
    register: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
    onStatus: (status: PushStatus) => states.push(status),
  };
  return { ...dependencies, states, session: new MobilePushSession(dependencies) };
}

describe("native APNs registration lifecycle", () => {
  it("checks existing permission without prompting, then enables on an explicit request", async () => {
    const f = fixture();
    vi.mocked(f.native.permission).mockResolvedValueOnce("undetermined");
    f.session.setUser("member");
    await f.session.refresh();
    expect(f.states.at(-1)).toBe("permission_required");
    expect(f.native.permission).toHaveBeenCalledWith(false);
    expect(f.register).not.toHaveBeenCalled();
    await f.session.refresh(true);
    expect(f.native.permission).toHaveBeenLastCalledWith(true);
    expect(f.register).toHaveBeenCalledWith({ topic: "test.app", environment: "sandbox", token: "abcd" }, expect.any(AbortSignal));
    expect(f.states.at(-1)).toBe("registered");
  });

  it("does not register a token that resolves after logout", async () => {
    const f = fixture();
    let resolveToken: (token: string) => void = () => { throw new Error("token not requested"); };
    vi.mocked(f.native.token).mockImplementation(() => new Promise((resolve) => { resolveToken = resolve; }));
    f.session.setUser("member");
    const registration = f.session.refresh();
    await vi.waitFor(() => expect(f.native.token).toHaveBeenCalled());
    f.session.setUser(null);
    resolveToken("abcd");
    await registration;
    expect(f.register).not.toHaveBeenCalled();
    expect(f.native.unregister).toHaveBeenCalledOnce();
  });

  it("starts removal before auth teardown and serializes native cleanup before the next registration", async () => {
    const f = fixture();
    f.session.setUser("member-a");
    await f.session.refresh();
    let release: () => void = () => { throw new Error("cleanup not started"); };
    vi.mocked(f.native.unregister).mockImplementation(() => new Promise((resolve) => { release = resolve; }));
    f.session.setUser(null);
    expect(f.remove).toHaveBeenCalledExactlyOnceWith({ topic: "test.app", environment: "sandbox", token: "abcd" });
    f.session.setUser("member-b");
    const next = f.session.refresh();
    await vi.waitFor(() => expect(f.native.unregister).toHaveBeenCalledOnce());
    expect(f.register).toHaveBeenCalledOnce();
    release();
    await next;
    expect(f.register).toHaveBeenCalledTimes(2);
  });

  it("rejects a server with the wrong app/environment before asking for permission", async () => {
    const f = fixture();
    f.configuration.mockResolvedValue({ enabled: true, topics: ["other.app"], environments: ["production"] });
    f.session.setUser("member");
    await f.session.refresh();
    expect(f.states.at(-1)).toBe("server_unavailable");
    expect(f.native.permission).not.toHaveBeenCalled();
  });

  it("exposes registration failure and retries the current device", async () => {
    const f = fixture();
    f.register.mockRejectedValueOnce(new Error("offline"));
    f.session.setUser("member");
    await f.session.refresh();
    expect(f.states.at(-1)).toBe("error");
    await f.session.refresh();
    expect(f.states.at(-1)).toBe("registered");
    expect(f.register).toHaveBeenCalledTimes(2);
  });
});
