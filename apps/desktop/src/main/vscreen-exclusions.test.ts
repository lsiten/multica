// @vitest-environment node
import { expect, it } from "vitest";
import { ownedMirrorWindowIDs } from "./vscreen-exclusions";
it("extracts only registered window IDs, deduplicates and bounds native values",()=>{
 expect(ownedMirrorWindowIDs(["window:41:0","window:42:1","window:41:0","screen:5:0"])).toEqual([41,42]);
 expect(()=>ownedMirrorWindowIDs(["window:4294967296:0"])).toThrow();
 expect(()=>ownedMirrorWindowIDs(Array.from({length:33},(_,i)=>`window:${i+1}:0`))).toThrow();
});
