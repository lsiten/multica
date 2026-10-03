import { expect,it,vi } from "vitest";
import { screen,waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { parseManagedWorktrees } from "@multica/core/types/managed-worktree";
import { emptyWorktreeFilters } from "@multica/core/types/worktree-filters";
import { renderWithI18n } from "../../test/i18n";
import { WorktreeInventoryFilters } from "./worktree-inventory-filters";

it("shares project and agent filtering while hiding a fixed workspace",async()=>{
 const rows=parseManagedWorktrees([{workspace_id:"ws",task_short:"task",path:"/root",kind:"issue",size_bytes:1,agent_id:"agent",agent_name:"Reviewer",project_id:"project",project_name:"Project",active:false}]);
 const change=vi.fn();
 const user=userEvent.setup();
 renderWithI18n(<WorktreeInventoryFilters rows={rows} filters={emptyWorktreeFilters} onChange={change} disabled={false} hideWorkspace />, {locale:"zh-Hans"});
 expect(screen.queryByRole("combobox",{name:"全部工作区"})).not.toBeInTheDocument();
 await user.click(screen.getByRole("combobox",{name:"全部智能体"}));
 await user.click(await screen.findByRole("option",{name:"Reviewer"}));
 await waitFor(()=>expect(change).toHaveBeenCalledWith({...emptyWorktreeFilters,agent:"agent"}));
 await user.click(screen.getByRole("combobox",{name:"全部项目"}));
 await user.click(await screen.findByRole("option",{name:"Project"}));
 await waitFor(()=>expect(change).toHaveBeenCalledWith({...emptyWorktreeFilters,project:"project"}));
});
