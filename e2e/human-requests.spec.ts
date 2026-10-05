import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

let api: TestApiClient;
test.beforeEach(async () => { api = await createTestApi(); });
test.afterEach(async () => { await api.cleanup(); });

test("manual request explains the operation and queues only one verification turn", async ({ page }) => {
  const workspace = await loginAsDefault(page);
  const issue = await api.createIssue("E2E actionable manual request");
  const request = await api.createHumanRequestFixture(issue.id, { key: "screen-recording", kind: "manual", title: "开启屏幕录制权限", steps: ["打开系统设置 → 隐私与安全性 → 屏幕录制，启用 Multica。"], action_label: "重新检查权限", verification: "Read screen-recording permission from the runtime before continuing", next: "我会检查实际权限，通过后继续。", details: "Technical evidence belongs in details." });
  await page.goto(`/${workspace}/issues/${issue.id}`, { waitUntil: "domcontentloaded" });
  const card = page.locator(`[data-human-request-id="${request.id}"]`);
  await expect(card.getByRole("heading", { name: "开启屏幕录制权限" })).toBeVisible();
  await expect(card.getByText("打开系统设置 → 隐私与安全性 → 屏幕录制，启用 Multica。")).toBeVisible();
  await expect(card.locator("details")).not.toHaveAttribute("open", "");
  await card.getByRole("button", { name: "重新检查权限" }).scrollIntoViewIfNeeded();
  await card.screenshot({ path: "/tmp/multica-human-request-desktop.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(card.getByRole("button", { name: "重新检查权限" })).toBeVisible();
  await card.screenshot({ path: "/tmp/multica-human-request-narrow.png" });
  const sent = page.waitForResponse(response => response.url().endsWith(`/human-requests/${request.id}/respond`) && response.request().method() === "POST");
  await card.getByRole("button", { name: "重新检查权限" }).click();
  const response = await sent;
  expect(response.status()).toBe(200);
  expect(response.request().postDataJSON()).toEqual({ revision: 1, decision: "completed" });
  await expect(card.getByRole("button", { name: "重新检查权限" })).toHaveCount(0);
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator(`[data-human-request-id="${request.id}"]`).getByRole("button", { name: "重新检查权限" })).toHaveCount(0);
});

test("failed input send retains the input and can be retried", async ({ page }) => {
  const workspace = await loginAsDefault(page);
  const issue = await api.createIssue("E2E actionable input request");
  const request = await api.createHumanRequestFixture(issue.id, { key: "test-site", kind: "input", title: "提供测试站点地址", input_label: "测试站点地址", action_label: "提交地址", next: "我会在该站点验证修改。" });
  await page.goto(`/${workspace}/issues/${issue.id}`, { waitUntil: "domcontentloaded" });
  const card = page.locator(`[data-human-request-id="${request.id}"]`);
  await card.getByLabel("测试站点地址").fill("https://test.example");
  await page.route(`**/human-requests/${request.id}/respond`, route => route.abort(), { times: 1 });
  await card.getByRole("button", { name: "提交地址" }).click();
  await expect(card.getByRole("alert")).toBeVisible();
  await expect(card.getByLabel("测试站点地址")).toHaveValue("https://test.example");
  const sent = page.waitForResponse(response => response.url().endsWith(`/human-requests/${request.id}/respond`) && response.status() === 200);
  await card.getByRole("button", { name: "提交地址" }).click();
  const response = await sent;
  expect(response.request().postDataJSON()).toEqual({ revision: 1, decision: "input", answer: "https://test.example" });
  await expect(card.getByLabel("测试站点地址")).toHaveCount(0);
});
