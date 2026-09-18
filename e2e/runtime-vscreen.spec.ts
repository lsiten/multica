import { test as base, expect, type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";
import {
  mockVscreenBrowserUI,
  CHILD_TASK_ID,
  PHYSICAL_LABEL,
  PHYSICAL_PIXEL,
  RUNTIME_ID,
  SOURCE_TASK_ID,
  VIRTUAL_LABEL,
  VIRTUAL_PIXEL,
  visibleVideoPixel,
} from "./runtime-vscreen-fixtures";

const worker = process.env.TEST_PARALLEL_INDEX ?? process.env.TEST_WORKER_INDEX ?? "0";
const run = process.env.E2E_RUN_ID ?? `${Date.now().toString(36)}-${process.pid.toString(36)}`;
const test = base.extend<{
  identity: { workspaceId: string; userId: string; slug: string };
}>({
  identity: async ({ context }, use) => {
    const api = new TestApiClient();
    try {
      const login = await api.login(`e2e-vscreen-${worker}-${run}@multica.ai`, "E2E Vscreen User");
      const workspace = await api.ensureWorkspace("E2E Vscreen", `e2e-vscreen-${worker}-${run}`);
      await api.markUserOnboarded();
      const token = api.getToken();
      const userId: string | undefined = login?.user?.id;
      if (!token || !userId) throw new Error("Vscreen browser UI login is incomplete");
      await context.addInitScript((authToken) => {
        localStorage.setItem("multica_token", authToken);
        localStorage.setItem("multica:chat:isOpen", "false");
      }, token);
      await use({ workspaceId: workspace.id, userId, slug: workspace.slug });
    } finally {
      await api.cleanup();
    }
  },
});

const surface = (page: Page) => page.getByRole("region", { name: "Runtime screen" });
const picker = (page: Page) => page.getByRole("combobox", { name: "Screen source" });
const mirrorStatus = (page: Page, text: string) => surface(page).getByRole("status").filter({ hasText: text }).first();
const handoff = (page: Page) => page.getByRole("region", { name: "Human takeover" });
async function openMirror(page: Page, slug: string) {
  await page.goto(`/${slug}/runtimes/${RUNTIME_ID}/mirror`, { waitUntil: "domcontentloaded" });
  await expect(picker(page)).toBeEnabled({ timeout: 15_000 });
}
async function expectSyntheticFrame(page: Page, label: string, pixel: number[]) {
  await expect(picker(page).locator("option:checked")).toHaveText(label);
  await expect(surface(page).getByText("Live · View only", { exact: true })).toBeVisible();
  await expect(surface(page).locator("video")).toBeVisible();
  await expect.poll(async () => {
    const actual = await visibleVideoPixel(page);
    return actual?.every((value, index) => Math.abs(value - pixel[index]) <= 3);
  }).toBe(true);
}

test.describe("Runtime virtual-screen browser UI (synthetic HTTP/RTC)", () => {
  test.use({ locale: "en-US" });
  test.beforeEach(async ({}, testInfo) => {
    testInfo.annotations.push({
      type: "acceptance-boundary",
      description: "Real shared browser route and auth; synthetic source catalog, grants and canvas RTC. Does not prove native media, AI target/lease, App control, Electron, displays or TCC.",
    });
  });
  test.afterEach(async ({ page }, testInfo) => {
    if (!page.isClosed()) {
      await testInfo.attach("browser-ui-final", {
        body: await page.screenshot({ fullPage: true }),
        contentType: "image/png",
      });
    }
  });

  test("two viewers independently select sources and leaving one mirror preserves the other", async ({ page, context, identity }, testInfo) => {
    const first = await mockVscreenBrowserUI(page, identity);
    const secondPage = await context.newPage();
    const second = await mockVscreenBrowserUI(secondPage, identity);
    try {
      await openMirror(page, identity.slug);
      await openMirror(secondPage, identity.slug);
      await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
      await expectSyntheticFrame(secondPage, VIRTUAL_LABEL, VIRTUAL_PIXEL);
      await picker(page).selectOption({ label: PHYSICAL_LABEL });
      await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
      await expectSyntheticFrame(secondPage, VIRTUAL_LABEL, VIRTUAL_PIXEL);
      expect(first.created.at(-1)?.request.viewer_id).not.toBe(second.created.at(-1)?.request.viewer_id);
      expect(second.created.map(({ request }) => request.source.kind)).toEqual(["virtual"]);
      expect(first.commands).toEqual([]);
      expect(second.commands).toEqual([]);
      await testInfo.attach("independent-viewer", { body: await secondPage.screenshot(), contentType: "image/png" });
      await secondPage.locator(`a[href="/${identity.slug}/runtimes"]`).last().click();
      await expect(secondPage).toHaveURL(new RegExp(`/${identity.slug}/runtimes$`), { timeout: 15_000 });
      await expect.poll(() => second.closed).toContain(second.created[0].id);
      await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    } finally {
      if (!secondPage.isClosed()) await secondPage.close();
    }
  });

  test("rapid source switches discard a late physical-session response", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    fixture.holdNextSource = "physical";
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expect.poll(() => fixture.held !== null).toBe(true);
    await picker(page).selectOption({ label: VIRTUAL_LABEL });
    await expect(surface(page).locator("video")).toHaveCount(0);
    const staleSession = await fixture.releaseHeld();
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    await expect.poll(() => fixture.closed).toContain(staleSession);
    expect(fixture.polled).not.toContain(staleSession);
    expect(fixture.created.map(({ request }) => request.source.kind)).toEqual(["virtual", "physical", "virtual"]);
    expect(fixture.commands).toEqual([]);
  });

  test("a disconnected selected source stays unavailable until the viewer explicitly chooses another", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    fixture.sources = ["physical"];
    await surface(page).getByRole("button", { name: "Reconnect", exact: true }).click();
    await expect(picker(page).locator("option:checked")).toHaveText("This screen is no longer available. Choose a current source.");
    await expect(surface(page).locator("video")).toHaveCount(0);
    await expect(mirrorStatus(page, "This screen is no longer available.")).toHaveText("This screen is no longer available. Choose a current source.");
    expect(fixture.created.some(({ request }) => request.source.kind === "physical")).toBe(false);
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    expect(fixture.commands).toEqual([]);
  });

  test("public runtime read access permits viewing but not lifecycle or takeover control", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity, { readerOnly: true });
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    await expect(surface(page).getByRole("button", { name: "Enable virtual screen", exact: true })).toBeDisabled();
    await expect(surface(page).getByRole("button", { name: "Disable virtual screen", exact: true })).toBeDisabled();
    await expect(surface(page).getByRole("button", { name: "Request takeover", exact: true })).toHaveCount(0);
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    expect(fixture.commands).toEqual([]);
  });

  test("disabled virtual screen stays unselected until an explicit source choice", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity, { disabled: true });
    await openMirror(page, identity.slug);
    await expect(surface(page).getByRole("button", { name: "Enable virtual screen", exact: true })).toBeEnabled();
    await expect(picker(page)).toHaveValue("");
    await expect(mirrorStatus(page, "Choose a screen")).toHaveText("Choose a screen");
    await expect(surface(page).locator("video")).toHaveCount(0);
    expect(fixture.created).toEqual([]);
    expect(fixture.commands).toEqual([]);
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    expect(fixture.commands).toEqual([]);
  });

  test("an empty source catalog explains the absence of a view without starting a session", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity);
    fixture.sources = [];
    await openMirror(page, identity.slug);
    await expect(mirrorStatus(page, "No authorized screens are available.")).toHaveText("No authorized screens are available.");
    await expect(picker(page)).toHaveValue("");
    await expect(picker(page).locator("option")).toHaveCount(1);
    await expect(surface(page).locator("video")).toHaveCount(0);
    expect(fixture.created).toEqual([]);
    expect(fixture.commands).toEqual([]);
  });

  test("screen-recording permission loss removes existing video and reports the reason", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    fixture.permission = "denied";
    fixture.stateRevision++;
    await expect(mirrorStatus(page, "Screen recording permission is required on this runtime.")).toHaveText("Screen recording permission is required on this runtime.", { timeout: 12_000 });
    await expect(surface(page).locator("video")).toHaveCount(0);
    await expect.poll(() => fixture.closed).toContain(fixture.created[0].id);
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expect(mirrorStatus(page, "Screen recording permission is required on this runtime.")).toHaveText("Screen recording permission is required on this runtime.");
    expect(fixture.created.some(({ request }) => request.source.kind === "physical")).toBe(false);
  });

  test("narrow layout contains a long runtime name and keeps source and reconnect controls usable", async ({ page, identity }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const runtimeName = "Browser UI runtime " + "long-runtime-name-".repeat(12);
    await mockVscreenBrowserUI(page, identity, { runtimeName });
    await openMirror(page, identity.slug);
    await expect(page.getByRole("heading", { name: runtimeName, exact: true })).toBeVisible();
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await expect(surface(page)).toBeInViewport();
    await expect(picker(page)).toBeInViewport();
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    await surface(page).getByRole("button", { name: "Reconnect", exact: true }).click();
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
  });

  test("web takeover sends an explicit request and never exposes local app-movement controls", async ({ page, identity }) => {
    const fixture = await mockVscreenBrowserUI(page, identity, { handoff: "request" });
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, VIRTUAL_LABEL, VIRTUAL_PIXEL);
    await expect(handoff(page).getByRole("status")).toHaveText("Ask the runtime owner to take over on its host.");
    expect(fixture.commands).toEqual([]);
    await handoff(page).getByRole("button", { name: "Request takeover", exact: true }).click();
    await expect(handoff(page).getByRole("status")).toHaveText("Waiting for local takeover");
    expect(fixture.commands).toEqual([{ command_id: expect.any(String), kind: "request_takeover" }]);
    await expect(handoff(page).getByRole("button", { name: SOURCE_TASK_ID.slice(0, 8), exact: true })).toBeVisible();
    await expect(handoff(page).getByRole("button", { name: "Move app here", exact: true })).toHaveCount(0);
    await expect(handoff(page).getByLabel("Move to display")).toHaveCount(0);
    await expect(handoff(page).getByRole("button", { name: "Return to virtual screen", exact: true })).toHaveCount(0);
    await expect(handoff(page).getByRole("button", { name: /settings$/ })).toHaveCount(0);
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    expect(fixture.commands).toHaveLength(1);
    expect(fixture.continuations).toEqual([]);
  });

  test("web continuation requires an explicit fresh-session choice after resume is unavailable", async ({ page, identity }, testInfo) => {
    const fixture = await mockVscreenBrowserUI(page, identity, { handoff: "ready_to_continue" });
    await openMirror(page, identity.slug);
    await expect(handoff(page).getByRole("status")).toHaveText("Returned and ready to continue");
    await expect(handoff(page).getByRole("button", { name: "Start a fresh session", exact: true })).toHaveCount(0);
    const summary = "Completed the requested sign-in in the test app.";
    await handoff(page).getByLabel("What changed?").fill(summary);
    await handoff(page).getByRole("button", { name: "Continue run", exact: true }).click();
    await expect(handoff(page).getByRole("alert")).toHaveText("The previous session cannot resume. You can explicitly start a fresh session.");
    expect(fixture.continuations).toEqual([{ human_summary: summary, fresh_session: false }]);
    await testInfo.attach("resume-unavailable-explicit-choice", { body: await page.screenshot(), contentType: "image/png" });
    await handoff(page).getByRole("button", { name: "Start a fresh session", exact: true }).click();
    await expect(handoff(page).getByRole("status")).toHaveText("Continued in a new run");
    expect(fixture.continuations).toEqual([
      { human_summary: summary, fresh_session: false },
      { human_summary: summary, fresh_session: true },
    ]);
    await expect(handoff(page).getByText("Original run:", { exact: false })).toBeVisible();
    await expect(handoff(page).getByText("Continuation run:", { exact: false })).toBeVisible();
    await expect(handoff(page).getByRole("button", { name: SOURCE_TASK_ID.slice(0, 8), exact: true })).toBeVisible();
    await expect(handoff(page).getByRole("button", { name: CHILD_TASK_ID.slice(0, 8), exact: true })).toBeVisible();
    expect(fixture.commands).toEqual([]);
  });
});
