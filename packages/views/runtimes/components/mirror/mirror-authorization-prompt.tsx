import { useEffect, useState } from "react";
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
  const operation = request.operation;
  const [expired, setExpired] = useState(false);
  useEffect(() => {
    const remaining = Date.parse(request.expires_at) - Date.now();
    setExpired(remaining <= 0);
    const timer = setTimeout(() => setExpired(true), Math.max(0, Math.min(remaining, 2_147_483_647)));
    return () => clearTimeout(timer);
  }, [request.request_id, request.expires_at]);
  const fileLabels = { add: t($ => $.vscreen.authorization_add_file), update: t($ => $.vscreen.authorization_update_file), delete: t($ => $.vscreen.authorization_delete_file), unknown: t($ => $.vscreen.authorization_file_change) };
  const title = operation?.kind === "command" ? t($ => $.vscreen.authorization_command_title) : operation?.kind === "files" ? t($ => $.vscreen.authorization_files_title) : operation?.kind === "permissions" ? t($ => $.vscreen.authorization_permissions_title) : request.title;
  const allow = operation?.kind === "command" ? t($ => $.vscreen.authorization_run_command) : operation?.kind === "files" ? t($ => $.vscreen.authorization_change_files) : operation?.kind === "permissions" ? t($ => $.vscreen.authorization_grant_permissions) : t($ => $.vscreen.authorization_allow);
  return (
    <Dialog open>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{operation?.kind === "permissions" ? t($ => $.vscreen.authorization_permissions_next) : operation ? t($ => $.vscreen.authorization_next) : request.message}</DialogDescription>
        </DialogHeader>
        {operation && <div className="space-y-3 overflow-auto">
          {operation.permissions ? <ul className="max-h-48 space-y-1 overflow-auto text-body">
 {operation.permissions.network_enabled !== undefined && <li>{operation.permissions.network_enabled ? t($ => $.vscreen.authorization_network_allow) : t($ => $.vscreen.authorization_network_deny)}</li>}
 {operation.permissions.read_paths?.map((path, index) => <li key={`read-${index}`} className="break-all">{t($ => $.vscreen.authorization_read_path)} <code className="font-mono text-caption">{JSON.stringify(path)}</code></li>)}
 {operation.permissions.write_paths?.map((path, index) => <li key={`write-${index}`} className="break-all">{t($ => $.vscreen.authorization_write_path)} <code className="font-mono text-caption">{JSON.stringify(path)}</code></li>)}
 </ul> : operation.files?.length ? <ul className="max-h-48 space-y-1 overflow-auto text-body">{operation.files.map((file, index) => <li key={index} className={file.kind === "delete" ? "break-all text-destructive" : "break-all"}>{fileLabels[file.kind]} <code className="font-mono text-caption">{JSON.stringify(file.path)}</code>{file.move_path && <span> → <code className="font-mono text-caption">{JSON.stringify(file.move_path)}</code></span>}</li>)}</ul> : <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 font-mono text-caption">{operation.target}</pre>}
          {operation.location && <p className="break-all text-caption">{t($ => $.vscreen.authorization_location)} {operation.location}</p>}
          {operation.reason && <p className="whitespace-pre-wrap break-words text-body">{operation.reason}</p>}
          <details><summary className="cursor-pointer text-caption">{t($ => $.vscreen.authorization_details)}</summary><pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-caption">{operation.details}</pre></details>
        </div>}
        {pending && <p role="status">{t(($) => $.vscreen.pending)}</p>}
        {failed && <p role="alert" className="text-destructive">{t(($) => $.vscreen.command_failed)}</p>}
        {expired && <p role="status" className="text-caption text-muted-foreground">{t($ => $.vscreen.authorization_expired)}</p>}
        <DialogFooter>
          <Button disabled={pending || expired} variant="outline" onClick={() => onDecision(false)}>{t(($) => $.vscreen.authorization_deny)}</Button>
          <Button disabled={pending || expired || operation?.kind === "unknown"} aria-busy={pending} onClick={() => onDecision(true)}>{allow}</Button>
          {(failed || expired) && <Button variant="outline" onClick={onDismiss}>{t(($) => $.vscreen.authorization_close)}</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
