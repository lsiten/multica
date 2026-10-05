import { test, expect as baseExpect } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

const expect = baseExpect.configure({ timeout: 30_000 });

let api: TestApiClient;
const evidence = resolve(process.env.MULTICA_PROGRESS_QA_DIR ?? "test-results/attention-actions");
test.beforeEach(async ({page}) => { page.on("pageerror",error=>console.log("Attention page error:",error.message)); page.on("requestfailed",request=>console.log("Attention request failed:",request.url(),request.failure()?.errorText)); api = await createTestApi(); await mkdir(evidence, { recursive: true }); });
test.afterEach(async ({ page }, info) => {
 if (info.status !== info.expectedStatus) { console.log("Attention QA failed page:", (await page.locator("body").innerText()).slice(-5000)); await page.screenshot({path:info.outputPath("failure.png")}); }
 await api.cleanup();
});
test.setTimeout(180_000);

test("a bound comment answer persists its receipt and exactly one continuation", async ({ page }) => {
  const workspace = await loginAsDefault(page);
  const issue = await api.createIssue("E2E dimensions choice", { status: "in_progress" });
  const request = await api.createHumanRequestFixture(issue.id, { key: "dimensions", kind: "choice", title: "接受原生1086×1448尺寸？", action_label: "提交尺寸选择", next: "继续交付四张图", response_mode: "chat_or_card", choices: [{ id: "native", label: "接受1086×1448原生尺寸" }, { id: "resize", label: "调整尺寸" }] });
  await page.goto(`/${workspace}/issues/${issue.id}`, { waitUntil: "domcontentloaded" });
  await expect(page.locator(`[data-human-request-id="${request.id}"]`).first()).toBeVisible();
  await page.getByRole("button", { name: "Leave a comment...", exact: true }).click();
  const editor = page.locator('[data-issue-comment-composer] [contenteditable="true"]');
  await editor.fill("A");
  await expect(page.getByText("Will confirm “接受原生1086×1448尺寸？”: 接受1086×1448原生尺寸", { exact: true })).toBeVisible();
  await page.screenshot({ path: resolve(evidence, "comment-bound-preview.png") });
  const replied = page.waitForResponse(response => response.url().endsWith(`/human-requests/${request.id}/reply`) && response.request().method() === "POST");
  await page.getByRole("button", { name: "Submit answer", exact: true }).click();
  expect((await replied).status()).toBe(200);
  await expect(page.locator(`[data-human-response="${request.id}"]`)).toHaveText("Confirmed: 接受1086×1448原生尺寸");
  expect(await api.fixtureRuns(issue.id)).toHaveLength(2);
  await expect(editor).toHaveText("");
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator(`[data-human-response="${request.id}"]`)).toBeVisible();
});

test("attention provides guarded continue, fresh rerun, missing fields and review acceptance", async ({ page }) => {
  const workspace = await loginAsDefault(page);
  const project = await api.createProject("E2E actionable attention");
  const delivered = await api.createIssue("E2E ended but unfinished", { status: "in_progress", project_id: project.id });
  const failed = await api.createIssue("E2E failed execution", { status: "in_progress", project_id: project.id });
  const info = await api.createIssue("E2E missing information", { status: "in_progress", project_id: project.id });
  const changes = await api.createIssue("E2E review changes", {status:"in_review",project_id:project.id});
  const review = await api.createIssue("E2E review delivery", { status: "in_review", project_id: project.id });
  await api.createProgressFixture(delivered.id, "completed");
  const failedRun = await api.createProgressFixture(failed.id, "failed");
  await api.createProgressFixture(info.id, "completed", { kind: "needs_information", summary: "请提供测试入口与验收条件", missing: ["测试站点地址", "验收条件"], evidence: ["登录后的验证等待入口"] });
  const reviewOwner = await api.createProgressFixture(review.id, "completed", { kind: "review", summary: "请检查交付成果", evidence: ["Focused validation passed"] });
  await api.createProgressFixture(changes.id,"completed",{kind:"review",summary:"Inspect the delivery before accepting",evidence:["Focused validation passed"]});
  const owned=await api.createIssue("E2E member-owned work",{status:"in_progress",project_id:project.id,assignee_type:"member",assignee_id:reviewOwner.userId});
  await page.goto(`/${workspace}/projects/${project.id}?project_tab=attention`, { waitUntil: "domcontentloaded" });
  const attention = page.getByRole("region", { name: "Needs attention", exact: true });
  const row = (id: string) => attention.locator(`[data-progress-issue-id="${id}"]`);
  await expect(row(delivered.id).getByText("Run ended; issue unfinished", { exact: true })).toBeVisible();
  await expect(row(delivered.id).getByRole("button", { name: "Rerun", exact: true })).toHaveCount(0);
  await row(delivered.id).getByRole("button", { name: "Inspect and continue", exact: true }).click();
  await row(delivered.id).getByRole("button", { name: "Submit operation", exact: true }).click();
  await expect.poll(async () => (await api.fixtureRuns(delivered.id)).length).toBe(2);
  expect((await api.fixtureRuns(delivered.id))[1]!.force_fresh_session).toBe(false);
  await row(failed.id).getByRole("button", { name: "Rerun", exact: true }).click();
  await expect(row(failed.id).getByText("Rerun starts a fresh agent session. It does not continue the previous session.", { exact: true })).toBeVisible();
  await row(failed.id).getByRole("button", { name: "Submit operation", exact: true }).click();
  await expect.poll(async () => (await api.fixtureRuns(failed.id)).length).toBe(2);
  expect((await api.fixtureRuns(failed.id))[1]).toMatchObject({ force_fresh_session: true, rerun_of_task_id: failedRun.taskId });
  await row(info.id).getByRole("button", { name: "Provide information", exact: true }).click();
  await row(info.id).getByRole("textbox", { name: "测试站点地址", exact: true }).fill("https://test.example");
  await expect(row(info.id).getByRole("button", { name: "Submit operation", exact: true })).toBeDisabled();
  await row(info.id).getByRole("textbox", { name: "验收条件", exact: true }).fill("四张图均通过验收");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await row(info.id).screenshot({ path: resolve(evidence, "attention-information-narrow.png") });
  await row(info.id).getByRole("button", { name: "Submit operation", exact: true }).click();
  await expect.poll(async () => (await api.fixtureRuns(info.id)).length).toBe(2);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await row(review.id).getByRole("button", { name: "Review delivery", exact: true }).click();
  await expect(row(review.id).getByText("Focused checks passed; delivery available for inspection.", { exact: true })).toBeVisible();
  await row(review.id).getByRole("button", { name: "Accept delivery", exact: true }).click();
  await expect(row(review.id)).toHaveCount(0);
  expect(await api.fixtureRuns(review.id)).toHaveLength(1);
  await row(changes.id).getByRole("button",{name:"Review delivery",exact:true}).click();
  await row(changes.id).getByRole("textbox",{name:"Review notes",exact:true}).fill("Add the missing narrow-screen evidence.");
  await row(changes.id).getByRole("button",{name:"Request changes",exact:true}).click();
  await expect.poll(async()=>(await api.fixtureRuns(changes.id)).length).toBe(2);
  expect((await api.fixtureRuns(changes.id))[1]!.force_fresh_session).toBe(false);
  await attention.getByRole("checkbox",{name:"Needs my action",exact:true}).check();
  await expect(row(owned.id).getByRole("button",{name:"Record my progress",exact:true})).toBeVisible();
  await row(owned.id).getByRole("button",{name:"Record my progress",exact:true}).click();
  await row(owned.id).getByRole("textbox").fill("Manual verification complete; results recorded.");
  await row(owned.id).getByRole("button",{name:"Submit operation",exact:true}).click();
  await expect(row(owned.id).getByRole("status")).toHaveText("Progress recorded.");
  expect(await api.fixtureRuns(owned.id)).toHaveLength(0);
  await attention.screenshot({ path: resolve(evidence, "attention-closure.png") });
});


test("chat records a bound choice while its source run is waiting, without an ordinary send", async ({page}) => {
 const workspace=await loginAsDefault(page);
 const {chatId,request}=await api.createChatChoiceFixture({key:"chat-size",kind:"choice",title:"Choose chat image size",action_label:"Submit size",next:"Continue delivery",response_mode:"chat_or_card",choices:[{id:"native",label:"Native dimensions"},{id:"resize",label:"Resize images"}]});
 await page.goto(`/${workspace}/chat?session=${chatId}`,{waitUntil:"domcontentloaded"});
 await expect(page.locator(`[data-human-request-id="${request.id}"]`).first()).toBeVisible();
 const input=page.locator('[data-slot="chat-input-surface"]:visible [contenteditable="true"]').first();await input.fill("A");
 await expect(page.getByText("Will confirm “Choose chat image size”: Native dimensions",{exact:true})).toBeVisible();
 const sent=page.waitForResponse(response=>response.url().endsWith(`/human-requests/${request.id}/reply`)&&response.request().method()==="POST");
 await page.getByRole("button",{name:"Submit answer",exact:true}).click();expect((await sent).status()).toBe(200);
 await expect(page.locator(`[data-human-response="${request.id}"]`)).toHaveText("Confirmed: Native dimensions");
 await page.screenshot({path:resolve(evidence,"chat-confirmed.png")});
});

test("multiple requests require an explicit target and unsupported replies retain the draft", async ({page}) => {
 const workspace=await loginAsDefault(page);
 const issue=await api.createIssue("E2E ambiguous choices",{status:"in_progress"});
 const payload={kind:"choice",action_label:"Submit choice",next:"Continue",response_mode:"chat_or_card",choices:[{id:"native",label:"Native dimensions"},{id:"resize",label:"Resize"}]};
 await api.createHumanRequestFixture(issue.id,{...payload,key:"first",title:"First choice"});
 const second=await api.createHumanRequestFixture(issue.id,{...payload,key:"second",title:"Second choice"});
 await page.goto(`/${workspace}/issues/${issue.id}`,{waitUntil:"domcontentloaded"});
 const composer=page.locator("[data-issue-comment-composer]");
 await expect(page.locator(`[data-human-request-id="${second.id}"]`).first()).toBeVisible();
 await composer.getByRole("button",{name:"Leave a comment...",exact:true}).click();
 const input=composer.locator('[contenteditable="true"]');await input.pressSequentially("A");await expect(input).toHaveText("A");
 await expect(composer.getByRole("combobox",{name:"Reply to request",exact:true}).getByRole("option",{name:"Second choice",exact:true})).toHaveCount(1);
 await composer.getByRole("button",{name:"Send",exact:true}).click();
 await expect(composer.getByRole("alert")).toHaveText("Select the request this answer belongs to before submitting.");
 expect(await api.fixtureRuns(issue.id)).toHaveLength(2);
 await composer.getByRole("combobox",{name:"Reply to request",exact:true}).selectOption(second.id);
 await expect(composer.getByText("Will confirm “Second choice”: Native dimensions",{exact:true})).toBeVisible();
 await page.route(`**/api/human-requests/${second.id}/reply`,route=>route.fulfill({status:404,contentType:"application/json",body:JSON.stringify({error:"unsupported"})}));
 await composer.getByRole("button",{name:"Submit answer",exact:true}).click();
 await expect(composer.getByRole("alert")).toBeVisible();await expect(input).toHaveText("A");
 expect(await api.fixtureRuns(issue.id)).toHaveLength(2);
 await page.screenshot({path:resolve(evidence,"bound-reply-error.png")});
 await page.unroute(`**/api/human-requests/${second.id}/reply`);
 await composer.getByRole("button",{name:"Submit answer",exact:true}).click();
 await expect(page.locator(`[data-human-response="${second.id}"]`)).toBeVisible();
 expect(await api.fixtureRuns(issue.id)).toHaveLength(3);
});


test("stale and expired decisions remain unconsumed in the real composer",async({page})=>{
 const workspace=await loginAsDefault(page);const issue=await api.createIssue("E2E decision revision",{status:"in_progress"});
 const request=await api.createHumanRequestFixture(issue.id,{key:"revision",kind:"choice",title:"Revision-bound choice",action_label:"Submit choice",next:"Continue",response_mode:"chat_or_card",choices:[{id:"native",label:"Native dimensions"},{id:"resize",label:"Resize"}]});
 await page.goto(`/${workspace}/issues/${issue.id}`,{waitUntil:"domcontentloaded"});const composer=page.locator("[data-issue-comment-composer]");await composer.getByRole("button",{name:"Leave a comment...",exact:true}).click();const input=composer.locator('[contenteditable="true"]');await input.fill("A");await expect(composer.getByText(/Will confirm/)).toBeVisible();
 await api.reviseHumanRequestFixture(request.id);
 await composer.getByRole("button",{name:"Submit answer",exact:true}).click();await expect(composer.getByRole("alert").first()).toContainText("latest version");await expect(input).toHaveText("A");expect(await api.fixtureRuns(issue.id)).toHaveLength(1);
 await composer.getByRole("button",{name:"Refresh request",exact:true}).click();await expect(composer.getByRole("alert")).toHaveCount(0);
 await api.reviseHumanRequestFixture(request.id,true);
 await composer.getByRole("button",{name:"Submit answer",exact:true}).click();await expect(composer.getByRole("alert").first()).toBeVisible();await expect(input).toHaveText("A");expect(await api.fixtureRuns(issue.id)).toHaveLength(1);
});
