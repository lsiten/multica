// @vitest-environment node
import { expect, it } from "vitest";
import { parseApplicationArguments, parseApplicationEnvironment } from "./display";

it("keeps spaces, empty arguments and equals signs without shell expansion", () => {
  expect(parseApplicationArguments('["--name", "hello world", "", "$(do-not-run)"]')).toEqual(["--name", "hello world", "", "$(do-not-run)"]);
  expect(parseApplicationEnvironment("URL=https://example.test?a=b\nNAME= value ")).toEqual({ URL: "https://example.test?a=b", NAME: " value " });
});

it("rejects ambiguous environment entries and invalid argument arrays", () => {
  expect(() => parseApplicationEnvironment("KEY=first\nKEY=second")).toThrow();
  expect(() => parseApplicationEnvironment("MULTICA_TOKEN=secret")).toThrow();
  expect(() => parseApplicationEnvironment("missing assignment")).toThrow();
  expect(() => parseApplicationArguments('{"program":"node"}')).toThrow();
});
