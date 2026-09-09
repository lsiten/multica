// Isolated IPC contract fixture: real preload and main request function,
// temporary userData, fake localhost daemon, no user profile or Git access.
const { app, BrowserWindow, ipcMain } = require("electron");
const { createServer } = require("node:http");
const { mkdirSync } = require("node:fs");
const { join } = require("node:path");

const fixtureRoot = process.argv[2];
if (!fixtureRoot || !fixtureRoot.startsWith("/tmp/multica-mr-ipc.")) throw new Error("isolated fixture root required");
mkdirSync(join(fixtureRoot, "userData"), { recursive: true });
app.setPath("userData", join(fixtureRoot, "userData"));
app.setName("Multica MR IPC Check");
app.commandLine.appendSwitch("remote-debugging-port", "9337");
const { requestLocalReview } = require(join(fixtureRoot, "request.cjs"));
let state = "draft";
let posts = 0;
const server = createServer((request, response) => {
  response.setHeader("Content-Type", "application/json");
  if (request.url === "/health") {
    response.end(JSON.stringify({ profile: "qa", workspaces: [{ id: "ws", runtimes: ["runtime"] }] }));
    return;
  }
  if (request.headers.authorization !== "Bearer isolated-fixture" || request.headers["x-multica-profile"] !== "qa") { response.writeHead(401).end(); return; }
  request.setEncoding("utf8");
  let body = "";
  request.on("data", (chunk) => { body += chunk; });
  request.on("end", () => {
    const input = JSON.parse(body);
    posts++;
    if (input.action === "approve") state = "approved";
    response.end(JSON.stringify({ id: "snapshot", path: "/fixture/repo", branch: "feature", target: "main", head: "head", target_head: "base", base: "base", dirty: false, branches: [], files: [], commits: "", review: { snapshot_id: "snapshot", state, comment: "", merged_commit: "" } }));
  });
});

app.whenReady().then(async () => {
  ipcMain.on("app:get-info", (event) => { event.returnValue = { version: "fixture", os: "macos" }; });
  ipcMain.on("runtime-config:get", (event) => { event.returnValue = { ok: false, error: { message: "Isolated IPC fixture" } }; });
  ipcMain.on("freeze:get-last", (event) => { event.returnValue = null; });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const profile = { name: "qa", port: server.address().port };
  ipcMain.handle("daemon:read-local-review", (_event, input) => requestLocalReview(input, {
    resolveProfile: async () => profile,
    health: async () => (await fetch(`http://127.0.0.1:${profile.port}/health`)).json(),
    review: async (selected, request) => (await fetch(`http://127.0.0.1:${selected.port}/worktrees/review`, {
      method: "POST", headers: { Authorization: "Bearer isolated-fixture", "X-Multica-Profile": selected.name }, body: JSON.stringify(request),
    })).json(),
  }));
  const window = new BrowserWindow({ show: false, width: 800, height: 500, webPreferences: {
    contextIsolation: true, nodeIntegration: false, sandbox: true, preload: join(fixtureRoot, "preload.cjs"),
  } });
  window.webContents.on("console-message", (_event, _level, message) => {
    if (message.startsWith("MR_IPC_RESULT")) console.log(message, "HTTP_POSTS=" + posts);
  });
  await window.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(`
    <!doctype html><title>MR IPC isolated check</title><h1>MR IPC isolated check</h1>
    <button id="run">Run IPC checks</button><pre id="result">Ready</pre>
    <script>
    document.querySelector('#run').onclick = async () => {
      try {
        const request = { task_id:'task',workspace_id:'ws',runtime_id:'runtime',path:'/fixture/repo',target:'main' };
        const read = await window.daemonAPI.readLocalReview(request);
        const approve = await window.daemonAPI.readLocalReview({...request,action:'approve',snapshot_id:read.id,command_id:'operation'});
        const remote = await window.daemonAPI.readLocalReview({...request,runtime_id:'elsewhere'});
        let rejected = false;
        try { await window.daemonAPI.readLocalReview({...request,action:'invalid'}); } catch { rejected = true; }
        const passed = read.review.state === 'draft' && approve.review.state === 'approved' && remote === null && rejected;
        const result = { passed, read:read.review.state, approve:approve.review.state, remote, malformedRejected:rejected };
        document.querySelector('#result').textContent = JSON.stringify(result);
        console.log('MR_IPC_RESULT ' + JSON.stringify(result));
      } catch(error) { document.querySelector('#result').textContent = String(error); }
    };
    </script>`));
  console.log("MR_IPC_READY pid=" + process.pid);
}).catch((error) => { console.error(error); app.exit(1); });
app.on("window-all-closed", () => app.quit());
app.on("before-quit", () => server.close());
process.on("SIGTERM", () => app.quit());
