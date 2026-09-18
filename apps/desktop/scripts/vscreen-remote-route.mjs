import { networkInterfaces, platform } from "node:os";
import { isIP, BlockList } from "node:net";
import { execFile } from "node:child_process";
import { lstat, readFile } from "node:fs/promises";
import { promisify } from "node:util";
const execute = promisify(execFile);
export async function remoteRoute(pair, dependencies = {}) {
  const unavailable = { available: false, kind: "unknown", reason: "physical_on_link_route_unavailable" };
  if (!pair?.local || !pair?.remote || !isIP(pair.local.address) || !isIP(pair.remote.address)) return unavailable;
  const interfaces = (dependencies.networkInterfaces ?? networkInterfaces)(), os = dependencies.platform ?? platform();
  const entries = Object.entries(interfaces), local = entries.find(([, values]) => values?.some((v) => v.address === pair.local.address && !v.internal));
  if (!local || entries.some(([, values]) => values?.some((v) => v.address === pair.remote.address)) || /^(lo|utun|tun|tap|wg|tailscale|docker|veth|br|bridge)/.test(local[0]) || !/^[A-Za-z0-9_.-]{1,64}$/.test(local[0])) return unavailable;
  const address = local[1].find((v) => v.address === pair.local.address), prefix = address?.cidr?.split("/")[1];
  if (!/^\d+$/.test(prefix ?? "") || isIP(pair.local.address) !== isIP(pair.remote.address)) return unavailable;
  const range = new BlockList(); try { const family = isIP(pair.local.address) === 6 ? "ipv6" : "ipv4"; range.addSubnet(pair.local.address, Number(prefix), family); if (!range.check(pair.remote.address, family)) return unavailable; } catch { return unavailable; }
  const run = (file, args) => (dependencies.execute ?? execute)(file, args, { timeout: 3000, maxBuffer: 65536 });
  try {
    if (os === "linux") {
      const stat = await (dependencies.lstat ?? lstat)(`/sys/class/net/${local[0]}/device`); if (!stat.isSymbolicLink()) return unavailable;
      const read = dependencies.readFile ?? readFile;
      const type = (await read(`/sys/class/net/${local[0]}/type`, "utf8")).trim(), flags = Number((await read(`/sys/class/net/${local[0]}/flags`, "utf8")).trim());
      if(type !== "1" || !Number.isInteger(flags) || (flags & 3) !== 3 || (flags & 0x18) !== 0) return unavailable;
      const result = JSON.parse((await run("/usr/sbin/ip", ["-json", isIP(pair.remote.address) === 6 ? "-6" : "-4", "route", "get", pair.remote.address])).stdout);
      if (result.length !== 1 || result[0].dev !== local[0] || result[0].gateway || (result[0].prefsrc ?? result[0].src) !== pair.local.address || result[0].type && result[0].type !== "unicast") return unavailable;
    } else if (os === "darwin") {
      const routes = (await run("/sbin/route", ["-n", "get", pair.remote.address])).stdout, ports = (await run("/usr/sbin/networksetup", ["-listallhardwareports"])).stdout;
      const hardware = ports.split(/\n\s*\n/).some((block) => /Hardware Port:.*(?:Wi-Fi|Ethernet)/.test(block) && block.split("\n").some((line) => line.trim() === `Device: ${local[0]}`));
      if (!hardware || routes.match(/interface:\s*(\S+)/)?.[1] !== local[0] || /flags:.*(?:GATEWAY|REJECT|BLACKHOLE)/.test(routes)) return unavailable;
    } else return unavailable;
    return { available: true, kind: "lan", interface_name: local[0], interface_addresses: local[1].map((v) => v.address), local_address: pair.local.address, destination: pair.remote.address, method: os === "linux" ? "ip-json-route-and-sysfs-device" : "route-and-networksetup-hardware-port" };
  } catch { return unavailable; }
}
