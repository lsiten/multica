import type { QueryClient } from "@tanstack/react-query";
import { issueKeys } from "@/data/queries/issue-keys";

export async function refreshIssuePages(client: QueryClient, workspaceId: string) {
  const queryKey = issueKeys.pages(workspaceId);
  // Cancel first loads too: invalidation alone can reuse a pre-event response.
  await client.cancelQueries({ queryKey });
  await client.invalidateQueries({ queryKey });
}
