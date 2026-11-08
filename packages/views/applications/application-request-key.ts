import { createSafeId } from "@multica/core/utils";
import type { ApplicationOperationRequest } from "@multica/core/applications";

export interface ApplicationRequestKey {
  readonly signature: string;
  readonly key: string;
}

interface OperationIntent {
  readonly workspaceId: string;
  readonly applicationId: string;
  readonly request: Omit<ApplicationOperationRequest, "idempotency_key">;
}

export function applicationRequestKey(previous: ApplicationRequestKey | null, intent: OperationIntent): ApplicationRequestKey {
  const { request } = intent;
  const signature = JSON.stringify([
    intent.workspaceId, intent.applicationId, request.action, request.revision,
    request.runtime_id, request.force ?? false,
    Object.entries(request.placements ?? {}).sort(([left], [right]) => left.localeCompare(right)),
  ]);
  return previous?.signature === signature ? previous : { signature, key: createSafeId() };
}
