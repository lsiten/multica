import { Button } from "@multica/ui/components/ui/button";
import type { MirrorAuthorizationRequest } from "./video-session";

export function MirrorAuthorizationPrompt({
  request,
  onDecision,
}: {
  readonly request: MirrorAuthorizationRequest;
  readonly onDecision: (approved: boolean) => void;
}) {
  return (
    <div role="dialog" aria-modal="true" aria-labelledby="mirror-authorization-title" className="absolute inset-x-4 top-4 z-10 rounded-lg border bg-background p-4 shadow-lg">
      <p id="mirror-authorization-title" className="font-medium">{request.title}</p>
      <p className="mt-1 text-caption text-muted-foreground">{request.message}</p>
      <div className="mt-3 flex justify-end gap-2">
        <Button size="sm" variant="outline" onClick={() => onDecision(false)}>拒绝</Button>
        <Button size="sm" onClick={() => onDecision(true)}>允许</Button>
      </div>
    </div>
  );
}
