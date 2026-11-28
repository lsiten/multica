"use client";

import { GitChangesPanel, type GitChangesRequest } from "../../issues/components/git-changes-panel";

export function ChatChangesPanel({ request }: {
  readonly request: GitChangesRequest;
  readonly onChanged: () => void;
  readonly onInvalidBinding: () => void;
}) {
  return <GitChangesPanel request={request} initialScope="uncommitted" compact />;
}
