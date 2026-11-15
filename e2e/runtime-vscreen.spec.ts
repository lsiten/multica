import { test as base, expect, type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";
import {
  mockMirrorBrowserUI,
  PHYSICAL_LABEL,
  PHYSICAL_PIXEL,
  RUNTIME_ID,
  SYSTEM_LABEL,
  SYSTEM_PIXEL,
  visibleVideoPixel,
} from "./runtime-vscreen-fixtures";

const worker =
  process.env.TEST_PARALLEL_INDEX ?? process.env.TEST_WORKER_INDEX ?? "0";
const run =
  process.env.E2E_RUN_ID ??
  `${Date.now().toString(36)}-${process.pid.toString(36)}`;
const test = base.extend<{
  identity: { workspaceId: string; userId: string; slug: string };
}>({
  identity: async ({ context }, use) => {
    const api = new TestApiClient();
    try {
      const login = await api.login(
        `e2e-mirror-${worker}-${run}@multica.ai`,
        "E2E Mirror User",
      );
      const workspace = await api.ensureWorkspace(
        "E2E Mirror",
        `e2e-mirror-${worker}-${run}`,
      );
      await api.markUserOnboarded();
      const token = api.getToken();
      const userId: string | undefined = login?.user?.id;
      if (!token || !userId) throw new Error("Mirror browser UI login is incomplete");
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
const picker = (page: Page) =>
  page.getByRole("combobox", { name: "Screen source" });
const mirrorStatus = (page: Page, text: string) =>
  surface(page).getByRole("status").filter({ hasText: text }).first();

async function openMirror(page: Page, slug: string) {
  await page.goto(`/${slug}/runtimes/${RUNTIME_ID}/mirror`, {
    waitUntil: "domcontentloaded",
  });
  await expect(picker(page)).toBeEnabled({ timeout: 15_000 });
}

async function expectSyntheticFrame(
  page: Page,
  label: string,
  pixel: number[],
) {
  await expect(picker(page).locator("option:checked")).toHaveText(label);
  await expect(
    surface(page).getByText("Live · View only", { exact: true }),
  ).toBeVisible();
  await expect(surface(page).locator("video")).toBeVisible();
  await expect
    .poll(async () => {
      const actual = await visibleVideoPixel(page);
      return actual?.every(
        (value, index) => Math.abs(value - pixel[index]) <= 3,
      );
    })
    .toBe(true);
}

test.describe("Runtime read-only mirror (synthetic HTTP/RTC)", () => {
  test.use({ locale: "en-US" });
  test.beforeEach(async ({}, testInfo) => {
    testInfo.annotations.push({
      type: "acceptance-boundary",
      description:
        "Real shared browser route and auth; synthetic source catalog, grants and canvas RTC. Does not prove native capture or agent-desktop.",
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

  test("two viewers independently switch physical and system sources", async ({
    page,
    context,
    identity,
  }, testInfo) => {
    const first = await mockMirrorBrowserUI(page, identity);
    const secondPage = await context.newPage();
    const second = await mockMirrorBrowserUI(secondPage, identity);
    try {
      await openMirror(page, identity.slug);
      await openMirror(secondPage, identity.slug);
      await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
      await expectSyntheticFrame(secondPage, PHYSICAL_LABEL, PHYSICAL_PIXEL);
      await picker(page).selectOption({ label: SYSTEM_LABEL });
      await expectSyntheticFrame(page, SYSTEM_LABEL, SYSTEM_PIXEL);
      await expectSyntheticFrame(secondPage, PHYSICAL_LABEL, PHYSICAL_PIXEL);
      expect(first.created.at(-1)?.request.viewer_id).not.toBe(
        second.created.at(-1)?.request.viewer_id,
      );
      expect(second.created.map(({ request }) => request.source.kind)).toEqual([
        "physical",
      ]);
      await testInfo.attach("independent-viewer", {
        body: await secondPage.screenshot(),
        contentType: "image/png",
      });
      await secondPage.locator(`a[href="/${identity.slug}/runtimes"]`).last().click();
      await expect(secondPage).toHaveURL(
        new RegExp(`/${identity.slug}/runtimes$`),
        { timeout: 15_000 },
      );
      await expect.poll(() => second.closed).toContain(second.created[0].id);
      await expectSyntheticFrame(page, SYSTEM_LABEL, SYSTEM_PIXEL);
    } finally {
      if (!secondPage.isClosed()) await secondPage.close();
    }
  });

  test("rapid source switches discard a late response", async ({
    page,
    identity,
  }) => {
    const fixture = await mockMirrorBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    fixture.holdNextSource = "system";
    await picker(page).selectOption({ label: SYSTEM_LABEL });
    await expect.poll(() => fixture.held !== null).toBe(true);
    await picker(page).selectOption({ label: PHYSICAL_LABEL });
    const staleSession = await fixture.releaseHeld();
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    await expect.poll(() => fixture.closed).toContain(staleSession);
    expect(fixture.polled).not.toContain(staleSession);
    expect(fixture.created.map(({ request }) => request.source.kind)).toEqual([
      "physical",
      "system",
      "physical",
    ]);
  });

  test("a removed source stays unavailable until another source is chosen", async ({
    page,
    identity,
  }) => {
    const fixture = await mockMirrorBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    fixture.sources = ["system"];
    await surface(page)
      .getByRole("button", { name: "Reconnect", exact: true })
      .click();
    await expect(picker(page).locator("option:checked")).toHaveText(
      "This screen is no longer available. Choose a current source.",
    );
    await expect(surface(page).locator("video")).toHaveCount(0);
    expect(fixture.created.some(({ request }) => request.source.kind === "system"))
      .toBe(false);
    await picker(page).selectOption({ label: SYSTEM_LABEL });
    await expectSyntheticFrame(page, SYSTEM_LABEL, SYSTEM_PIXEL);
  });

  test("the mirror exposes no control or takeover affordances", async ({
    page,
    identity,
  }) => {
    await mockMirrorBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    await expect(
      surface(page).getByRole("button", {
        name: /control|takeover|return|remote interaction/i,
      }),
    ).toHaveCount(0);
    await expect(
      surface(page).getByRole("textbox", { name: /type|send/i }),
    ).toHaveCount(0);
  });

  test("an empty source catalog explains the absence without a session", async ({
    page,
    identity,
  }) => {
    const fixture = await mockMirrorBrowserUI(page, identity);
    fixture.sources = [];
    await openMirror(page, identity.slug);
    await expect(
      mirrorStatus(page, "No authorized screens are available."),
    ).toHaveText("No authorized screens are available.");
    await expect(picker(page)).toHaveValue("");
    await expect(surface(page).locator("video")).toHaveCount(0);
    expect(fixture.created).toEqual([]);
  });

  test("screen-recording permission loss removes existing video", async ({
    page,
    identity,
  }) => {
    const fixture = await mockMirrorBrowserUI(page, identity);
    await openMirror(page, identity.slug);
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    fixture.permission = "denied";
    await expect(
      mirrorStatus(
        page,
        "Screen recording permission is required on this runtime.",
      ),
    ).toHaveText("Screen recording permission is required on this runtime.", {
      timeout: 12_000,
    });
    await expect(surface(page).locator("video")).toHaveCount(0);
    await expect.poll(() => fixture.closed).toContain(fixture.created[0].id);
  });

  test("narrow layout keeps source and reconnect controls usable", async ({
    page,
    identity,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const runtimeName =
      "Browser UI runtime " + "long-runtime-name-".repeat(12);
    await mockMirrorBrowserUI(page, identity, { runtimeName });
    await openMirror(page, identity.slug);
    await expect(
      page.getByRole("heading", { name: runtimeName, exact: true }),
    ).toBeVisible();
    await expectSyntheticFrame(page, PHYSICAL_LABEL, PHYSICAL_PIXEL);
    await expect
      .poll(() =>
        page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      )
      .toBe(true);
    await expect(surface(page)).toBeInViewport();
    await expect(picker(page)).toBeInViewport();
    await picker(page).selectOption({ label: SYSTEM_LABEL });
    await expectSyntheticFrame(page, SYSTEM_LABEL, SYSTEM_PIXEL);
    await surface(page)
      .getByRole("button", { name: "Reconnect", exact: true })
      .click();
    await expectSyntheticFrame(page, SYSTEM_LABEL, SYSTEM_PIXEL);
  });
});
