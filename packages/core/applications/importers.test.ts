// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseApplicationImport } from "./importers";

describe("application importers", () => {
  it("imports package scripts", () => {
    expect(parseApplicationImport("package", JSON.stringify({ scripts: { start: "node server.js --port 4100" } }))).toMatchObject({ command: ["node", "server.js", "--port", "4100"], port: 4100 });
  });

  it("imports the web process from Procfile", () => {
    expect(parseApplicationImport("procfile", "worker: node worker.js\nweb: npm start -p 3000")).toMatchObject({ command: ["npm", "start", "-p", "3000"], port: 3000 });
  });

  it("imports JSON and YAML Compose services", () => {
    expect(parseApplicationImport("compose", JSON.stringify({ services: { web: { command: ["node", "server.js"], ports: ["3000:8080"], environment: { NODE_ENV: "production" } } } }))).toMatchObject({ command: ["node", "server.js"], environment: { NODE_ENV: "production" }, port: 8080 });
    expect(parseApplicationImport("compose", "services:\n  web:\n    command: python -m http.server 8000\n    ports:\n      - \"8000:8000\"\n    environment:\n      MODE: prod")).toMatchObject({ port: 8000, environment: { MODE: "prod" } });
  });
});
