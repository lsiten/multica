import { test, expect as baseExpect, _electron } from "@playwright/test";
import { createRequire } from "node:module";
import { basename, resolve } from "node:path";
import { mkdir, rm } from "node:fs/promises";
import { createTestApi } from "./helpers";

const expect = baseExpect.configure({timeout:30_000});

test("desktop preserves direct child operations and follows deep work through the real tab router", async () => {
  test.skip(process.env.MULTICA_RUN_DESKTOP_PROGRESS_QA !== "1", "Requires the locally built Electron renderer");
  test.setTimeout(150_000);
  const api = await createTestApi();
  const project = await api.createProject("Desktop progress project");
  const root = await api.createIssue("Desktop progress goal", { status: "in_progress", project_id: project.id });
  const branch = await api.createIssue("Desktop integration branch", { status: "in_progress", parent_issue_id: root.id, project_id: project.id });
  const leaf = await api.createIssue("Desktop deep unfinished work", { status: "blocked", parent_issue_id: branch.id, project_id: project.id });
  const prerequisite = await api.createIssue("Desktop repair prerequisite", { status: "todo", project_id: project.id });
  const intermediate = await api.createIssue("Desktop integration prerequisite", { status: "todo", project_id: project.id });
  await api.createIssueDependency(leaf.id, intermediate.id);
  await api.createIssueDependency(intermediate.id, prerequisite.id);
  const decision = await api.createHumanRequestFixture(branch.id, { key: "native", kind: "choice", title: "接受原生尺寸？", action_label: "提交尺寸", next: "继续交付", response_mode: "chat_or_card", choices: [{ id: "native", label: "接受1086×1448原生尺寸" }, { id: "resize", label: "调整尺寸" }] });
  const continuation = await api.createIssue("Desktop ended work needing action", {status:"in_progress",project_id:project.id});
  await api.createProgressFixture(continuation.id,"completed");
  const workspace = (await api.getWorkspaces()).find(workspace => workspace.slug.startsWith("e2e-workspace-"));
  if (!workspace || !api.getToken()) throw new Error("Desktop fixture auth was not prepared");
  const require = createRequire(resolve("apps/desktop/package.json"));
  const electronPath: string = require("electron");
  const suffix = `issue-progress-e2e-${process.pid}-${Date.now()}`;
  const app = await _electron.launch({ executablePath: electronPath, args: [resolve("apps/desktop")], env: { ...process.env, NODE_ENV: "development", DESKTOP_APP_SUFFIX: suffix, MULTICA_DEV_PROFILE: suffix, ELECTRON_RENDERER_URL: "" }, timeout: 60_000 });
  let userData: string | undefined;
  let renderer: Awaited<ReturnType<typeof app.firstWindow>> | undefined;
  try {
    userData = await app.evaluate(({ app }) => app.getPath("userData"));
    expect(basename(userData)).toBe(`Multica Canary ${suffix}`);
    const page = await app.firstWindow();
    renderer = page;
    page.on("pageerror", error => console.log("Desktop progress QA browser error:", error.message));
    await app.evaluate(({ ipcMain }) => {
      for (const channel of ["daemon:set-target-api-url", "daemon:auto-start", "daemon:sync-token", "daemon:clear-token"]) {
        ipcMain.removeHandler(channel);
        ipcMain.handle(channel, async () => undefined);
      }
    });
    await page.waitForLoadState("domcontentloaded");
    await page.evaluate(({ token }) => {
      localStorage.setItem("multica_token", token);
      localStorage.setItem("multica-locale", "en");
      localStorage.setItem("multica:chat:isOpen", "false");
    }, { token: api.getToken()! });
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByText("Desktop progress goal", { exact: true }).click({ timeout: 30_000 });
    const progress = page.locator("[data-issue-work-progress]");
    await expect(progress.getByText("Unfinished: 2", { exact: true })).toBeVisible({ timeout: 30_000 });
    await expect(progress.getByRole("button", { name: "Direct sub-issues", exact: true })).toHaveAttribute("aria-pressed", "true");
    await progress.getByRole("button", { name: "Remaining work", exact: true }).click();
    await expect(progress.locator(`[data-progress-issue-id="${leaf.id}"]`).getByText("Final prerequisites", { exact: true })).toBeVisible();
    const evidenceDir = resolve(process.env.MULTICA_PROGRESS_QA_DIR ?? "test-results/issue-progress-evidence");
    await mkdir(evidenceDir, { recursive: true });
    await progress.screenshot({ path: resolve(evidenceDir, "electron-remaining.png") });
    await api.updateIssue(leaf.id, { status: "done" });
    await expect(progress.getByText("Unfinished: 1", { exact: true })).toBeVisible({ timeout: 15_000 });
    await page.locator('a[href$="/projects"]').filter({ hasText: "Projects" }).first().click();
    await page.getByText("Desktop progress project", { exact: true }).last().click();
    await page.getByRole("button", { name: "Needs attention", exact: true }).click();
    const attention = page.getByRole("region", { name: "Needs attention", exact: true });
    await expect(attention.locator(`[data-progress-issue-id="${branch.id}"]`)).toBeVisible({ timeout: 30_000 });
    await attention.getByRole("checkbox", { name: "Needs my action", exact: true }).check();
    await expect(attention.getByRole("checkbox", { name: "Needs my action", exact: true })).toBeChecked();
    await attention.getByRole("checkbox", { name: "Needs my action", exact: true }).uncheck();
    await expect(attention.locator(`[data-progress-issue-id="${branch.id}"]`)).toBeVisible();
    await page.screenshot({ path: resolve(evidenceDir, "electron-attention.png") });
    const actionRow=attention.locator(`[data-progress-issue-id="${continuation.id}"]`);
    await actionRow.getByRole("button",{name:"Inspect and continue",exact:true}).click();
    await actionRow.getByRole("button",{name:"Submit operation",exact:true}).click();
    await expect.poll(async()=>(await api.fixtureRuns(continuation.id)).length).toBe(2);
    expect((await api.fixtureRuns(continuation.id))[1]!.force_fresh_session).toBe(false);
    await attention.locator(`[data-progress-issue-id="${branch.id}"]`).getByRole("link",{name:/Desktop integration branch/}).last().click();
    await page.getByRole("button",{name:"Leave a comment...",exact:true}).click();
    const editor=page.locator('[data-issue-comment-composer] [contenteditable="true"]');
    await editor.fill("1");
    await expect(page.getByText("Will confirm “接受原生尺寸？”: 接受1086×1448原生尺寸",{exact:true})).toBeVisible();
    await editor.focus();
    await page.keyboard.press("Meta+Enter");
    await expect(page.locator(`[data-human-response="${decision.id}"]`)).toBeVisible();
    expect(await api.fixtureRuns(branch.id)).toHaveLength(2);
    await page.getByRole("button",{name:"Search...",exact:true}).click();
    await page.getByPlaceholder("Type a command or search...").fill("dark");
    await page.getByText("Switch to Dark Theme",{exact:true}).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.screenshot({path:resolve(evidenceDir,"electron-bound-answer-dark.png"),animations:"disabled"});

  } catch (error) {
    if (renderer && !renderer.isClosed()) {
      await renderer.screenshot({ path: test.info().outputPath("electron-failure.png") });
      console.log("Desktop progress QA page:", (await renderer.locator("body").innerText()).slice(0, 3000));
    }
    throw error;
  } finally {
    await app.close();
    if (userData && basename(userData) === `Multica Canary ${suffix}`) await rm(userData, { recursive: true, force: true });
    await api.cleanup();
  }
});
