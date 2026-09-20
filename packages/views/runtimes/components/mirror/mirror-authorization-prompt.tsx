import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
    <Dialog open>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{request.title}</DialogTitle>
          <DialogDescription>{request.message}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => onDecision(false)}>拒绝</Button>
          <Button onClick={() => onDecision(true)}>允许</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
