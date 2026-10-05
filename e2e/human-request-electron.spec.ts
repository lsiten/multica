import { test, expect, _electron } from "@playwright/test";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import { createTestApi } from "./helpers";

test("desktop renders the request and submits the exact decision", async () => {
  test.skip(process.env.MULTICA_RUN_DESKTOP_HUMAN_REQUEST_QA !== "1", "Requires the locally built Electron renderer");
  const api = await createTestApi();
  const issue = await api.createIssue("E2E desktop human decision");
  const request = await api.createHumanRequestFixture(issue.id, { key:"desktop-choice",kind:"choice",title:"选择验证范围",choices:[{id:"focused",label:"运行相关测试",recommended:true},{id:"all",label:"运行全部测试"}],action_label:"提交选择",next:"我会按你选择的范围验证。" });
  const require = createRequire(resolve("apps/desktop/package.json"));
  const electronPath: string = require("electron");
  const app = await _electron.launch({ executablePath:electronPath,args:[resolve("apps/desktop")],env:{...process.env,NODE_ENV:"development",DESKTOP_APP_SUFFIX:"human-request-e2e",ELECTRON_RENDERER_URL:""},timeout:60000 });
  try {
    const page = await app.firstWindow();
    // Keep the real desktop renderer, router and API. Agent execution is an
    // external dependency: these fixtures must never start the user's CLIs.
    await app.evaluate(({ipcMain}) => {
      for (const channel of ["daemon:set-target-api-url","daemon:auto-start","daemon:sync-token","daemon:clear-token"]) {
        ipcMain.removeHandler(channel);
        ipcMain.handle(channel,async()=>undefined);
      }
    });
    await page.waitForLoadState("domcontentloaded");
    const workspace = (await api.getWorkspaces()).find(workspace => workspace.slug.startsWith("e2e-workspace-"));
    if (!workspace || !api.getToken()) throw new Error("Desktop fixture auth was not prepared");
    await page.evaluate(({token,slug}) => {
      localStorage.setItem("multica_token",token);
      localStorage.setItem("multica-locale","zh-Hans");
      location.hash=`/${slug}/issues`;
    },{token:api.getToken()!,slug:workspace.slug});
    await page.reload({waitUntil:"domcontentloaded"});
    await expect(page.getByRole("heading",{name:"任务",exact:true})).toBeVisible({timeout:30000});
    await page.evaluate(({slug,id})=>{location.hash=`/${slug}/issues/${id}`},{slug:workspace.slug,id:issue.id});
    const card=page.locator(`[data-human-request-id="${request.id}"]`);
    await expect(card.getByRole("heading",{name:"选择验证范围"})).toBeVisible({timeout:30000});
    await card.getByRole("button",{name:"运行全部测试"}).scrollIntoViewIfNeeded();
    await card.screenshot({path:"/tmp/multica-human-request-electron-card.png"});
    const sent=page.waitForResponse(response=>response.url().endsWith(`/human-requests/${request.id}/respond`)&&response.request().method()==="POST");
    await card.getByRole("button",{name:"运行全部测试"}).click();
    const response=await sent;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toEqual({revision:1,decision:"choice",answer:"all"});
    await expect(card.getByRole("button",{name:"运行全部测试"})).toHaveCount(0);
  } finally {
    await app.close();
    await api.cleanup();
  }
});
