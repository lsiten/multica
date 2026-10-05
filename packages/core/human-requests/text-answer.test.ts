// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { HumanRequest } from "../types/human-request";
import { matchHumanTextAnswer } from "./text-answer";

function request(): HumanRequest {
  return { id:"request",workspace_id:"ws",source_task_id:"run",agent_id:"agent",recipient_id:"me",issue_id:"issue",chat_session_id:null,project_id:null,revision:3,status:"pending",response:null,response_task_id:null,can_respond:true,expires_at:new Date(Date.now()+60_000).toISOString(),payload:{key:"size",kind:"choice",title:"Size",steps:[],action_label:"Submit",next:"Continue",response_mode:"chat_or_card",choices:[{id:"native",label:"接受1086×1448原生尺寸"},{id:"resize",label:"重新调整尺寸"}] } };
}

describe("version-bound text answer",()=>{
  it.each(["A","a","1","选第1项","选择1","接受1086×1448原生尺寸"," A "])("matches the complete answer %s",text=>{
    expect(matchHumanTextAnswer(request(),text)).toEqual({revision:3,decision:"choice",answer:"native"});
  });
  it.each(["不是A","不要选1","A还是B？","A，但修改尺寸","5","A/B","他说选1",""])("does not turn discussion %s into a decision",text=>{
    expect(matchHumanTextAnswer(request(),text)).toBeNull();
  });
  it("requires explicit binding for free input",()=>{
    const input=request();input.payload.kind="input";input.payload.choices=[];
    expect(matchHumanTextAnswer(input,"https://test.example")).toBeNull();
    expect(matchHumanTextAnswer(input,"https://test.example",true)?.answer).toBe("https://test.example");
  });
  it("does not guess duplicate labels or consume card-only/other-member/expired requests",()=>{
    const pending=request();pending.payload.choices[1]!.label=pending.payload.choices[0]!.label;
    expect(matchHumanTextAnswer(pending,pending.payload.choices[0]!.label)).toBeNull();
    pending.payload.response_mode="card_only";expect(matchHumanTextAnswer(pending,"A")).toBeNull();
    pending.payload.response_mode="chat_or_card";pending.can_respond=false;expect(matchHumanTextAnswer(pending,"A")).toBeNull();
    pending.can_respond=true;pending.expires_at="2000-01-01T00:00:00Z";expect(matchHumanTextAnswer(pending,"A")).toBeNull();
  });
});
