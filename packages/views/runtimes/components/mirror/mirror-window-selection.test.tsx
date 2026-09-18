// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithI18n } from "../../../test/i18n";
import { MirrorWindowSelection } from "./mirror-window-selection";
const scope={backendIdentity:"https://fixture.invalid",accountId:"owner",workspaceId:"workspace",runtimeId:"runtime"};
const candidates={windows:[{handle:"opaque-choice",title:"Private draft",bundleId:"org.example.Editor"}],truncated:false};
afterEach(()=>vi.useRealTimers());
it("lists only on explicit click, never preselects, and confirms only the opaque handle",async()=>{
 const control=vi.fn().mockResolvedValueOnce({ok:true,local:true,candidates}).mockResolvedValueOnce({ok:true,local:true});const adopted=vi.fn().mockResolvedValue(undefined);
 renderWithI18n(<MirrorWindowSelection scope={scope} interventionId="intervention" disabled={false} control={control} onAdopted={adopted}/>);
 expect(control).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"List local windows"}));
 const select=await screen.findByLabelText("App window");expect(select).toHaveValue("");expect(screen.getByRole("button",{name:"Confirm selected window"})).toBeDisabled();
 fireEvent.change(select,{target:{value:"opaque-choice"}});expect(control).toHaveBeenCalledTimes(1);
 fireEvent.click(screen.getByRole("button",{name:"Confirm selected window"}));
 await waitFor(()=>expect(adopted).toHaveBeenCalledOnce());expect(control).toHaveBeenLastCalledWith(scope,{action:"adopt_window",interventionId:"intervention",windowHandle:"opaque-choice"});expect(screen.queryByText("Private draft · org.example.Editor")).toBeNull();
});
it("expires candidate titles and handles in component memory",async()=>{
 const control=vi.fn().mockResolvedValue({ok:true,local:true,candidates});renderWithI18n(<MirrorWindowSelection scope={scope} interventionId="intervention" disabled={false} control={control} onAdopted={async()=>{}}/>);
 vi.useFakeTimers();await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"List local windows"}));});expect(screen.getByLabelText("App window")).toBeInTheDocument();
 await act(async()=>{vi.advanceTimersByTime(10_001);});expect(screen.queryByLabelText("App window")).toBeNull();expect(screen.getByRole("alert")).toHaveTextContent("selection expired");expect(control).toHaveBeenCalledTimes(1);
});
it("failed adoption clears the candidate without replay and asks for a fresh list",async()=>{
 const control=vi.fn().mockResolvedValueOnce({ok:true,local:true,candidates}).mockResolvedValueOnce({ok:false,local:true,reason:"selection_expired"});const adopted=vi.fn();
 renderWithI18n(<MirrorWindowSelection scope={scope} interventionId="intervention" disabled={false} control={control} onAdopted={adopted}/>);
 fireEvent.click(screen.getByRole("button",{name:"List local windows"}));fireEvent.change(await screen.findByLabelText("App window"),{target:{value:"opaque-choice"}});fireEvent.click(screen.getByRole("button",{name:"Confirm selected window"}));
 await screen.findByRole("alert");expect(control).toHaveBeenCalledTimes(2);expect(adopted).not.toHaveBeenCalled();expect(screen.queryByLabelText("App window")).toBeNull();
});
