// @vitest-environment node
import { expect, it } from "vitest";
import { configureRendererBackend, createRendererWebPreferences } from "./renderer-web-preferences";

it("isolates backend sessions and reuses the partition when returning", () => {
  configureRendererBackend("https://one.example");
  const first = createRendererWebPreferences("preload", "en").partition;
  configureRendererBackend("https://two.example");
  const second = createRendererWebPreferences("preload", "en").partition;
  expect(second).not.toBe(first);
  expect(second).toMatch(/^persist:backend-/);
  configureRendererBackend("https://one.example");
  expect(createRendererWebPreferences("preload", "en").partition).toBe(first);
});
