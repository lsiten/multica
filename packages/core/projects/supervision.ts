import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const projectSupervisionKey=(wsId:string,projectId:string)=>["project_supervision",wsId,projectId] as const;
export function projectSupervisionOptions(wsId:string,projectId:string){
  return queryOptions({queryKey:projectSupervisionKey(wsId,projectId),queryFn:async()=>{
    const view=await api.getProjectSupervision(projectId);
    if(view.workspace_id!==wsId)throw new Error("Supervision response belongs to another workspace");
    return view;
  },refetchInterval:30_000});
}
