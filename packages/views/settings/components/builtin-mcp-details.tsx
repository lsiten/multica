"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { parseBuiltinMcpServices } from "@multica/core/api";
import { useCurrentWorkspace } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { McpReadinessBadge } from "./mcp-readiness-status";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { AppLink, useOptionalNavigation } from "../../navigation";
import { settingsHref } from "./settings-navigation";

export function BuiltinMcpDetailsButton({name}:{name:string}){
 const {t}=useT("settings");const workspace=useCurrentWorkspace();const navigation=useOptionalNavigation();
 const [open,setOpen]=useState(false);
 const daemon=typeof window==="undefined"?undefined:(window as unknown as {daemonAPI?:{getBuiltinMcpServices?:()=>Promise<unknown>}}).daemonAPI;
 const query=useQuery({queryKey:["builtin-mcp-details",workspace?.id??""],enabled:open&&Boolean(daemon?.getBuiltinMcpServices),queryFn:async()=>parseBuiltinMcpServices(await daemon?.getBuiltinMcpServices?.()),refetchInterval:open?5_000:false});
 const service=query.data?.services.find(item=>item.name===name);
 const instances=(query.data?.instances??[]).filter(item=>item.name===name&&(!item.workspace_id||item.workspace_id===workspace?.id));
 return <Dialog open={open} onOpenChange={setOpen}>
  <DialogTrigger render={<Button size="sm" variant="outline" aria-label={`${t($=>$.jev.view_details)} · ${name}`}/>}>{t($=>$.jev.view_details)}</DialogTrigger>
  <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
   <DialogHeader><DialogTitle>{name}</DialogTitle><DialogDescription>{t($=>$.jev.service_details_hint)}</DialogDescription></DialogHeader>
   {!daemon?.getBuiltinMcpServices?<p role="status" className="text-caption">{t($=>$.jev.daemon_unavailable)}</p>:query.isPending?<p role="status">{t($=>$.jev.loading)}</p>:query.isError?<div role="alert" className="space-y-2 text-caption"><p>{query.error.message}</p><Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.jev.retry)}</Button></div>:service?<div className="space-y-4">
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-caption"><dt>{t($=>$.jev.transport)}</dt><dd>{service.transport}</dd><dt>{t($=>$.jev.authorization)}</dt><dd>{service.requires_capability?t($=>$.jev.identity_authorization):t($=>$.jev.provider_context)}</dd></dl>
    <section className="space-y-2"><h3 className="text-caption font-medium">{t($=>$.jev.service_instances)}</h3>{instances.length?instances.map(instance=><div key={instance.instance_id??instance.scope} className="rounded-md border p-3 text-caption"><div className="flex flex-wrap items-center justify-between gap-2"><span>{instance.scope} · {instance.instance_id??"—"}</span><McpReadinessBadge state={instance.state}/></div>{instance.reason&&<p className="mt-1 break-words text-muted-foreground">{instance.reason}</p>}<p className="mt-1 text-muted-foreground">{t($=>$.jev.instance_diagnostics,{tools:instance.tool_count??"—",checked:instance.checked_at?new Date(instance.checked_at).toLocaleString():"—"})}</p></div>):<p className="text-caption text-muted-foreground">{t($=>$.jev.daemon_unavailable)}</p>}</section>
    <section className="space-y-2"><h3 className="text-caption font-medium">{t($=>$.jev.service_tools)}</h3>{service.tools.map(tool=><details key={tool.name} className="rounded-md border p-3"><summary className="cursor-pointer break-all text-caption font-medium">{tool.name}</summary><p className="mt-2 text-caption text-muted-foreground">{tool.description}</p><pre className="mt-2 max-h-64 overflow-auto rounded-md bg-muted p-2 text-caption">{JSON.stringify(tool.inputSchema,null,2)}</pre></details>)}</section>
    {name==="multica-llm2jev"&&navigation&&<AppLink className="text-caption underline" href={settingsHref(navigation.pathname,navigation.searchParams,"jev")}>{t($=>$.jev.open_configuration)}</AppLink>}
   </div>:<p role="alert">{t($=>$.jev.daemon_invalid)}</p>}
  </DialogContent>
 </Dialog>;
}
