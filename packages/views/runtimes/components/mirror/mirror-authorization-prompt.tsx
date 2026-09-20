import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";
import type { MirrorAuthorizationRequest } from "./video-session";

export function MirrorAuthorizationPrompt({
  request,
  onDecision,
  pending,
  failed,
  onDismiss,
}: {
  readonly pending: boolean;
  readonly failed: boolean;
  readonly onDismiss: () => void;
  readonly request: MirrorAuthorizationRequest;
  readonly onDecision: (approved: boolean) => void;
}) {
  const { t } = useT("runtimes");
  return (
    <Dialog open>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{request.title}</DialogTitle>
          <DialogDescription>{request.message}</DialogDescription>
        </DialogHeader>
        {pending && <p role="status">{t(($) => $.vscreen.pending)}</p>}
        {failed && <p role="alert" className="text-destructive">{t(($) => $.vscreen.command_failed)}</p>}
        <DialogFooter>
          <Button disabled={pending || failed} variant="outline" onClick={() => onDecision(false)}>{t(($) => $.vscreen.authorization_deny)}</Button>
          <Button disabled={pending || failed} aria-busy={pending} onClick={() => onDecision(true)}>{t(($) => $.vscreen.authorization_allow)}</Button>
          {failed && <Button variant="outline" onClick={onDismiss}>{t(($) => $.vscreen.close)}</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
