import { expect, test } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

const serviceConfig = {
  mode: "external", command: [], prepare: [], environment: {}, local_env: {}, port: 4200,
  health: { kind: "tcp", path: "", timeout_seconds: 60, interval_seconds: 5 },
  restart: { enabled: false, max_attempts: 3, delay_seconds: 5, restore: false }, auto_publish: false, entry_path: "/",
};

test.describe("Applications module", () => {
  let api: TestApiClient;
  let projectId: string;
  let serviceId: string;
  const suffix = Date.now().toString(36);
  const projectName = `Applications ${suffix}`;
  const serviceName = `Preview API ${suffix}`;
  const compositionName = `Website ${suffix}`;

  test.beforeEach(async () => {
    api = await createTestApi();
    projectId = (await api.createProject(projectName)).id;
    serviceId = (await api.createApplication(projectId, serviceName, "service", serviceConfig)).id;
  });
  test.afterEach(async () => { await api.cleanup(); });

  test("persists dependency URL bindings in the application editor", async ({ page }) => {
    test.setTimeout(150000);
    const resource = await api.createApplicationProjectResource(projectId);
    const source = await api.createApplication(projectId, `Connected frontend ${suffix}`, "service", { ...serviceConfig, mode: "managed", resource_id: resource.id, command: ["test-created-application-command"] });
    const slug = await loginAsDefault(page);
    await page.goto(`/${slug}/applications/${source.id}`, { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "Edit application", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.locator("summary").click();
    await dialog.getByRole("button", { name: "Add connection", exact: true }).click();
    await dialog.getByRole("combobox", { name: "Target application", exact: true }).click();
    await page.getByRole("option", { name: serviceName, exact: true }).click();
    await dialog.getByRole("textbox", { name: "URL environment variable", exact: true }).fill("API_URL");
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).toBeHidden();
    const saved = await (await api.applicationRequest(`/${source.id}`)).json() as { config: { connections: { target_id: string; url_variable: string }[] } };
    expect(saved.config.connections).toEqual([{ target_id: serviceId, url_variable: "API_URL" }]);
    await page.getByRole("button", { name: "Edit application", exact: true }).click();
    await dialog.locator("summary").click();
    await expect(dialog.getByRole("textbox", { name: "URL environment variable", exact: true })).toHaveValue("API_URL");
    await page.setViewportSize({ width: 390, height: 844 });
    await dialog.getByRole("textbox", { name: "URL environment variable", exact: true }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: "_attachments/applications-connections-editor-mobile.png", fullPage: true });
    await dialog.getByRole("button", { name: "Remove connection", exact: true }).click();
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).toBeHidden();
  });

  test("confirms cancellation and shows reconciliation before the terminal result", async ({ page }) => {
    test.setTimeout(150000);
    const operationId = "55555555-5555-4555-8555-555555555555";
    const runtimeId = "44444444-4444-4444-8444-444444444444";
    let state = "queued";
    let receipt: Record<string, unknown>;
    await page.route("**/api/applications/board", async (route) => {
      const response = await route.fetch();
      const board = await response.json();
      const application = board.applications.find((app: { id: string }) => app.id === serviceId);
      receipt = { id: operationId, workspace_id: application.workspace_id, application_id: serviceId, action: "start", actor_type: "member", actor_id: application.created_by,
        state, created_at: "2026-10-07T01:00:00Z", cancel_requested_at: state === "queued" ? null : "2026-10-07T01:01:00Z",
        cancel_actor_type: "member", cancel_actor_id: state === "queued" ? "" : application.created_by,
        snapshot: { root_runtime_id: runtimeId, root_revision: 1, placements: { [serviceId]: runtimeId }, plan: { root_id: serviceId, nodes: [], waves: [] } },
        steps: [{ id: operationId, instance_id: runtimeId, application_id: serviceId, runtime_id: runtimeId, generation: 1, wave: 0, required: true, action: "start", state: state === "queued" ? "queued" : "cancelled", error: "" }] };
      board.operations = [receipt];
      await route.fulfill({ response, json: board });
    });
    await page.route(`**/api/applications/${serviceId}/operations/${operationId}/cancel`, async (route) => {
      expect(route.request().method()).toBe("POST");
      state = "cancelling";
      await route.fulfill({ status: 202, json: { ...receipt, state, cancel_requested_at: "2026-10-07T01:01:00Z" } });
    });
    const slug = await loginAsDefault(page);
    await page.goto(`/${slug}/applications/${serviceId}`, { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: serviceName })).toBeVisible({ timeout: 30000 });
    await page.getByRole("button", { name: "Activity", exact: true }).click();
    await page.getByRole("button", { name: "Cancel operation", exact: true }).click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog.getByText("Unfinished starts are stopped; running and shared members stay available. Unexecuted stops are withdrawn; stops already executing finish shutting down.")).toBeVisible();
    await dialog.getByRole("button", { name: "Cancel operation", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText("Cancelling", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Cancel operation", exact: true })).toBeHidden();
    state = "cancelled";
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "Activity", exact: true }).click();
    await expect(page.getByText("Cancelled", { exact: true }).first()).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: "_attachments/applications-cancellation-mobile.png", fullPage: true });
  });

  test("continues log pages, pauses polling and downloads the loaded output", async ({ page }) => {
	test.setTimeout(150000);
	const instanceId = "33333333-3333-4333-8333-333333333333";
	let reads = 0;
	await page.route(`**/api/applications/${serviceId}`, async (route) => {
	  const response = await route.fetch();
	  const application = await response.json();
	  application.config = { ...application.config, mode: "managed", command: ["node", "server.js"] };
	  await route.fulfill({ response, json: application });
	});
	await page.route("**/api/applications/board", async (route) => {
	  const response = await route.fetch();
	  const board = await response.json();
	  const application = board.applications.find((app: { id: string }) => app.id === serviceId);
	  board.instances = [{ id: instanceId, workspace_id: application.workspace_id, application_id: serviceId,
	    runtime_id: "44444444-4444-4444-8444-444444444444", revision: 1, generation: 1, observed_generation: 1,
	    observed_revision: 1, desired_state: "stopped", process_state: "stopped", health_state: "unknown",
	    runtime_state: "online", status: "stopped", can_manage: true }];
	  await route.fulfill({ response, json: board });
	});
	await page.route(`**/api/applications/${serviceId}/instances/${instanceId}/logs?**`, async (route) => {
	  reads++;
	  const cursor = new URL(route.request().url()).searchParams.get("cursor");
	  await route.fulfill({ json: cursor === "" ? { text: "first event\n", cursor: "host:0:12", gap: false }
	    : cursor === "host:0:12" ? { text: "second event\n", cursor: "host:0:25", gap: false }
	    : { text: "", cursor: "host:0:25", gap: false } });
	});
	const slug = await loginAsDefault(page);
	await page.goto(`/${slug}/applications/${serviceId}`, { waitUntil: "domcontentloaded" });
	await expect(page.getByRole("heading", { name: serviceName })).toBeVisible({ timeout: 30000 });
	await page.getByRole("button", { name: "Logs", exact: true }).click();
	const output = page.locator("pre");
	await expect(output).toHaveText("first event\nsecond event\n");
	await page.clock.install();
	await page.getByRole("button", { name: "Pause logs", exact: true }).click();
	await expect(page.getByRole("button", { name: "Resume logs", exact: true })).toBeVisible();
	const pausedReads = reads;
	await page.clock.runFor(6000);
	expect(reads).toBe(pausedReads);
	await page.getByRole("textbox", { name: "Search logs", exact: true }).fill("second");
	await expect(output).toHaveText("second event");
	const downloaded = page.waitForEvent("download");
	await page.getByRole("button", { name: "Download loaded logs", exact: true }).click();
	const download = await downloaded;
	expect(download.suggestedFilename()).toContain(instanceId);
	const stream = await download.createReadStream();
	const chunks: Buffer[] = [];
	for await (const chunk of stream!) chunks.push(Buffer.from(chunk));
	expect(Buffer.concat(chunks).toString()).toBe("first event\nsecond event\n");
	await page.getByRole("button", { name: "Resume logs", exact: true }).click();
	await page.clock.runFor(3000);
	await expect.poll(() => reads).toBeGreaterThan(pausedReads);
	await page.setViewportSize({ width: 390, height: 844 });
	await page.getByRole("textbox", { name: "Search logs", exact: true }).fill("");
	await expect(output).toHaveText("first event\nsecond event\n");
	await page.screenshot({ path: "_attachments/applications-logs-mobile.png", fullPage: true });
  });

  test("creates a composition, edits relationships and shows all board views", async ({ page }) => {
    test.setTimeout(150000);
    const slug = await loginAsDefault(page);
    await page.getByRole("link", { name: "Applications", exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/${slug}/applications`), { timeout: 30000 });
    await expect(page.getByRole("heading", { name: "Applications", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: serviceName }).first()).toBeVisible();
    await page.getByRole("button", { name: "New application", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("textbox", { name: "Name", exact: true }).fill(compositionName);
    await dialog.getByRole("combobox", { name: "Project", exact: true }).click();
    await page.getByRole("option", { name: projectName, exact: true }).click();
    await dialog.getByRole("combobox", { name: "Type", exact: true }).click();
    await page.getByRole("option", { name: "Composition", exact: true }).click();
    const created = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/api/applications/"));
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    const response = await created;
    expect(response.status()).toBe(201);
    const composition = await response.json() as { id: string };
    api.trackApplication(composition.id);
    await expect(dialog).toBeHidden();
    await page.getByRole("link", { name: compositionName }).first().click();
    await expect(page).toHaveURL(new RegExp(`/applications/${composition.id}`), { timeout: 30000 });
    await expect(page.getByRole("heading", { name: compositionName })).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    await page.getByRole("button", { name: "Add relationship", exact: true }).click();
    await page.getByRole("combobox", { name: "Target application", exact: true }).click();
    await page.getByRole("option", { name: serviceName, exact: true }).click();
    await page.getByRole("button", { name: "Save relationships", exact: true }).click();
    await expect(page.getByText("Wave 1", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: serviceName }).first()).toBeVisible();
    await page.getByRole("link", { name: "Applications", exact: true }).last().click();
    await page.getByRole("button", { name: "Runtimes", exact: true }).click();
    await page.getByRole("button", { name: "Run board", exact: true }).click();
    await expect(page.getByText("Stopped", { exact: true }).first()).toBeVisible();
    await page.getByRole("button", { name: "Projects", exact: true }).click();
    await page.getByRole("textbox", { name: "Search applications", exact: true }).fill(serviceName);
    await expect(page.getByRole("link", { name: serviceName }).first()).toBeVisible();
    await page.screenshot({ path: "_attachments/applications-project-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole("heading", { name: "Applications", exact: true })).toBeVisible();
    await page.screenshot({ path: "_attachments/applications-project-mobile.png", fullPage: true });
    await page.goto(`/${slug}/applications/${serviceId}`, { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: serviceName })).toBeVisible();
    await page.getByRole("button", { name: "Edit application", exact: true }).click();
    await expect(page.getByRole("dialog").getByText("Registration provides health checks and access; it does not own the service process.")).toBeVisible();
    await page.waitForFunction(() => {
      const dialog = document.querySelector('[role="dialog"]');
      return !!dialog && getComputedStyle(dialog).opacity === "1" && dialog.getAnimations().every((animation) => animation.playState === "finished");
    });
    await page.screenshot({ path: "_attachments/applications-editor-mobile.png", fullPage: true });
    await page.getByRole("dialog").getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(page.getByRole("dialog")).toBeHidden();
    await page.evaluate(() => localStorage.setItem("theme", "dark"));
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: serviceName })).toBeVisible();
    await page.waitForFunction(() => document.documentElement.classList.contains("dark") && !document.documentElement.classList.contains("light"));
    await page.screenshot({ path: "_attachments/applications-detail-mobile-dark.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`/${slug}/applications`, { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: "Applications", exact: true })).toBeVisible();
    await page.waitForFunction(() => document.documentElement.classList.contains("dark") && !document.documentElement.classList.contains("light"));
    await page.screenshot({ path: "_attachments/applications-project-desktop-dark.png", fullPage: true });
  });
});
