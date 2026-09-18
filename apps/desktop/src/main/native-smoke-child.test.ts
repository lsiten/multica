// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
const mocks=vi.hoisted(()=>({spawn:vi.fn()}));
vi.mock("node:child_process",()=>({spawn:mocks.spawn}));
import { runSmokeChild } from "./native-smoke";
afterEach(()=>{vi.useRealTimers();vi.clearAllMocks();});
function child(){const emitter=new EventEmitter();return Object.assign(emitter,{pid:123,stdout:new PassThrough(),stderr:new PassThrough(),kill:vi.fn()});}
it("bounds cancellation, signals only its owned child and retains forced-cleanup uncertainty",async()=>{
 vi.useFakeTimers();const owned=child();mocks.spawn.mockReturnValue(owned);
 const result=runSmokeChild("/owned/helper",["internal-vscreen-diagnostics"],100,new AbortController().signal,false);
 await vi.advanceTimersByTimeAsync(100);expect(owned.kill).toHaveBeenCalledWith("SIGTERM");
 await vi.advanceTimersByTimeAsync(25000);expect(owned.kill).toHaveBeenCalledWith("SIGKILL");
 owned.emit("close",0);expect(await result).toMatchObject({closed:true,forced:true,aborted:true});
 expect(mocks.spawn.mock.calls[0]?.[2].env).toEqual({PATH:"/usr/bin:/bin:/usr/sbin:/sbin",LANG:"en_US.UTF-8"});
});
it("bounds hostile output and never inherits private invocation capability",async()=>{
 const owned=child();mocks.spawn.mockReturnValue(owned);const result=runSmokeChild("/owned/helper",[],1000,new AbortController().signal,false);
 owned.stdout.write(Buffer.alloc(1024*1024+1));expect(owned.kill).toHaveBeenCalledWith("SIGTERM");owned.emit("close",1);
 expect(await result).toMatchObject({aborted:true,stdout:""});expect(mocks.spawn.mock.calls[0]?.[2].env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE).toBeUndefined();
});
it("does not claim a child was reaped after a spawn error",async()=>{
 const owned=child();mocks.spawn.mockReturnValue(owned);const result=runSmokeChild("/missing/helper",[],1000,new AbortController().signal,false);owned.emit("error",new Error("missing"));expect(await result).toMatchObject({closed:false,code:null});
});
