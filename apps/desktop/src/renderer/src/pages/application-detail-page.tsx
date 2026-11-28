import { useParams } from "react-router-dom";
import { ApplicationDetail } from "@multica/views/applications";

export function ApplicationDetailPage() {
  const { id } = useParams<{ id: string }>();
  return id ? <ApplicationDetail applicationId={id} /> : null;
}
