import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ApplicationDetail } from "@multica/views/applications";
import { applicationDetailOptions } from "@multica/core/applications";
import { useWorkspaceId } from "@multica/core/hooks";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function ApplicationDetailPage() {
  const { id } = useParams<{ id: string }>();
  const workspaceId = useWorkspaceId();
  const application = useQuery({ ...applicationDetailOptions(workspaceId, id ?? ""), enabled: !!id });
  useDocumentTitle(application.data?.name ?? "Application");
  return id ? <ApplicationDetail applicationId={id} /> : null;
}
