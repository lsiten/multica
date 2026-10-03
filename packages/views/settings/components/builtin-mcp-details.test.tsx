// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { BuiltinMcpDetailsButton } from "./builtin-mcp-details";
vi.mock("@multica/core/paths",()=>({useCurrentWorkspace:()=>({id:"workspace"})}));
afterEach(()=>{delete (window as unknown as {daemonAPI?:unknown}).daemonAPI});
function mount(){return renderWithI18n(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><BuiltinMcpDetailsButton name="multica-identity-actions"/></QueryClientProvider>)}
describe("built-in MCP service details",()=>{
 it("loads public tool contracts and current workspace instances only after opening",async()=>{
  const get=vi.fn().mockResolvedValue({services:[{name:"multica-identity-actions",transport:"streamable_http",requires_capability:true,tools:[{name:"multica_identity_send_email",description:"Send an email",inputSchema:{type:"object",required:["recipient"]}}]}],instances:[{name:"multica-identity-actions",enabled:true,scope:"daemon",ready:true,state:"broker_ready",instance_id:"broker"},{name:"multica-identity-actions",workspace_id:"other",enabled:true,scope:"task",ready:true,state:"ready",instance_id:"private-other-task"}]});
  Object.defineProperty(window,"daemonAPI",{configurable:true,value:{getBuiltinMcpServices:get}});
  mount();expect(get).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:/View details/}));
  expect(await screen.findByRole("dialog")).toBeInTheDocument();
  expect(await screen.findByText("multica_identity_send_email")).toBeInTheDocument();
  expect(screen.getByText("Explicit task identity permission is required.")).toBeInTheDocument();
  expect(screen.queryByText(/private-other-task/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByText("multica_identity_send_email"));expect(screen.getByText(/recipient/)).toBeInTheDocument();
 });
 it("reports an unavailable desktop daemon without pretending the service is ready",async()=>{
  mount();fireEvent.click(screen.getByRole("button",{name:/View details/}));
  expect(await screen.findByText("The local daemon is unavailable.")).toBeInTheDocument();
 });
});
