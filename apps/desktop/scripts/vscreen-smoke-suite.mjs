import { mkdir, access, writeFile } from "node:fs/promises";
import { join } from "node:path";

export const REQUIRED_SMOKE_SCENARIOS = Object.freeze(["lifecycle", "sources", "video", "background-input", "takeover", "performance"]);
export function canonicalSmokeScenario(value) { return ({ source:"sources", input:"background-input" })[value] ?? value; }
export function nativeSmokeScenario(value) { return ({ sources:"source", "background-input":"input" })[canonicalSmokeScenario(value)] ?? value; }

export function aggregateSmokeResults(children) {
  const missing=REQUIRED_SMOKE_SCENARIOS.filter((scenario)=>children.filter((child)=>child.scenario===scenario).length!==1);
  const failed=children.filter((child)=>child.status!=="passed").map((child)=>({scenario:child.scenario,reason:child.error?.code ?? "child_gate_failed"}));
  const coverageRequirements={"background-input":["positivePIDActions","userAppCompatibility","continuousForegroundTyping"],takeover:["desktopUI","productionDB","manualHandoff"],performance:["lanAcceptance"]};
  const missingCoverage=[];
  for(const [scenario,gates]of Object.entries(coverageRequirements)){const child=children.find((item)=>item.scenario===scenario);for(const gate of gates)if(child?.planCoverage?.[gate]!=="verified")missingCoverage.push({scenario,gate,status:child?.planCoverage?.[gate]??"unverified"});}
  return {status:missing.length||failed.length||missingCoverage.length?"blocked":"passed",required:[...REQUIRED_SMOKE_SCENARIOS],missing,failed,missingCoverage};
}

// A failure is retained for every gate. Cleanup uncertainty prevents another GUI
// launch, while later gates remain explicitly blocked rather than silently skipped.
export async function runSmokeSuite(options, runChild) {
  await mkdir(options.evidence,{recursive:true});
  const path=join(options.evidence,"report.json");
  try{await access(path);throw new Error("Evidence directory already contains report.json");}catch(error){if(error.code!=="ENOENT")throw error;}
  const report={schema_version:1,scenario:"all",status:"blocked",started_at:new Date().toISOString(),gui_exercised:false,children:[],limitations:["All includes every required scenario; no unsupported, permission, cleanup, decode or metric gate may be omitted."]};
  let unsafe=false;
  for(const scenario of REQUIRED_SMOKE_SCENARIOS){
    if(!options.allowGui||unsafe){report.children.push({scenario,status:"blocked",error:{code:unsafe?"not_attempted_cleanup_unconfirmed":"gui_not_authorized"}});continue;}
    const directory=join(options.evidence,scenario);
    let result;
    try{result=await runChild({...options,scenario,evidence:directory});}catch(error){result={scenario,status:"blocked",error:{code:error.code??"scenario_runner_failed"}};unsafe=true;}
    report.children.push({scenario,status:result.status,report:join(directory,"report.json"),error:result.error,gui_exercised:result.gui_exercised===true,planCoverage:result.performance?.assessment?.planCoverage??result.planCoverage});
    report.gui_exercised ||= result.gui_exercised===true;
    if((result.gui_exercised || result.gui_attempted) && (result.gui?.disposed!==true||result.gui?.host_closed!==true))unsafe=true;
  }
  Object.assign(report,aggregateSmokeResults(report.children));report.finished_at=new Date().toISOString();
  await writeFile(path,JSON.stringify(report,null,2)+"\n");return report;
}
