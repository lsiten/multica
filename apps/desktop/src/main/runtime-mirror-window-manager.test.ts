// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
vi.mock("electron", () => ({}));
import { mirrorWindowBounds } from "./runtime-mirror-window-manager";
const workArea = { x: 1920, y: 28, width: 1440, height: 872 };
describe("Floating mirror geometry", () => {
  it("starts at the upper right of the actual work area", () => {
    // Given / When
    const bounds = mirrorWindowBounds(workArea);
    // Then
    expect(bounds).toMatchObject({ x: 2984, y: 44, width: 360 });
    expect(bounds.height).toBeGreaterThan((360 * 9) / 16);
  });
  it.each([
    [20, 240],
    [2000, 720],
  ])("bounds requested width %s to %s", (requested, expected) => {
    // Given / When
    const bounds = mirrorWindowBounds(workArea, requested);
    // Then
    expect(bounds.width).toBe(expected);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(
      workArea.x + workArea.width,
    );
  });
});

import type { BrowserWindow, IpcMainInvokeEvent } from "electron";
import { RuntimeMirrorWindowManager } from "./runtime-mirror-window-manager";
it("local handoff accepts only registered top-frame current account/backend context",()=>{
  const frame={url:"https://desktop.test/"};const webContents={mainFrame:frame};const main={webContents} as unknown as BrowserWindow;
  let account="owner",generation=1;
  const manager=new RuntimeMirrorWindowManager({mainWindow:()=>main,accountId:()=>account,generation:()=>generation,backendIdentity:()=>"https://backend.test",rendererURL:()=>"https://desktop.test/",preloadPath:"/fixture/preload",register:()=>{},unregister:()=>{}});
  const scope={backendIdentity:"https://backend.test",accountId:"owner",workspaceId:"workspace",runtimeId:"runtime"};
  const event={sender:webContents,senderFrame:frame} as unknown as IpcMainInvokeEvent;
  expect(manager.localContext(event,scope)?.generation).toBe(1);
  expect(manager.localContext({...event,senderFrame:{url:frame.url}} as IpcMainInvokeEvent,scope)).toBeNull();
  expect(manager.localContext({...event,sender:{}} as IpcMainInvokeEvent,scope)).toBeNull();
  expect(manager.localContext(event,{...scope,backendIdentity:"https://remote.test"})).toBeNull();
  account="other";expect(manager.localContext(event,scope)).toBeNull();account="owner";generation=2;
  expect(manager.localContext(event,scope)?.generation).not.toBe(1);
});
