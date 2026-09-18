// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
const f=vi.hoisted(()=>({normal:vi.fn(),smoke:vi.fn().mockResolvedValue(0),exit:vi.fn()}));
vi.mock("electron",()=>({app:{exit:f.exit}}));
vi.mock("node:module",()=>({createRequire:()=>f.normal}));
vi.mock("./native-smoke",()=>({runDesktopNativeSmoke:f.smoke}));
afterEach(()=>{vi.unstubAllEnvs();vi.resetModules();vi.clearAllMocks();});
it("loads normal startup synchronously when test mode is absent",async()=>{vi.stubEnv("MULTICA_DESKTOP_NATIVE_SMOKE_FILE",undefined);await import("./index");expect(f.normal).toHaveBeenCalledWith("./normal-startup.js");expect(f.smoke).not.toHaveBeenCalled();});
it("never imports product startup even for an invalid smoke invocation",async()=>{vi.stubEnv("MULTICA_DESKTOP_NATIVE_SMOKE_FILE","");await import("./index");expect(f.normal).not.toHaveBeenCalled();expect(f.smoke).toHaveBeenCalledWith(expect.anything(),"",{entryPath:expect.any(String)});});
