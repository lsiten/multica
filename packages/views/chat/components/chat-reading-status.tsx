import { Loader2 } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export function ChatReadingStatus({ pending, failed, onRetry, onLatest }: {
  readonly pending: boolean;
  readonly failed: boolean;
  readonly onRetry: () => void;
  readonly onLatest: () => void;
}) {
  const { t } = useT("chat");
  if (!pending && !failed) return null;
  return <div role={failed ? "alert" : "status"} className={pending ? "absolute inset-0 z-10 flex flex-wrap items-center justify-center gap-2 bg-background p-4 text-caption text-muted-foreground" : "flex shrink-0 flex-wrap items-center gap-2 px-4 py-2 text-caption text-muted-foreground"}>
    {pending && <Loader2 className="size-4 animate-spin motion-reduce:animate-none" aria-hidden="true" />}
    <span>{t(($) => failed ? $.message_list.restore_failed : $.message_list.restore_loading)}</span>
    {failed && <Button variant="ghost" size="sm" onClick={onRetry}>{t(($) => $.message_list.restore_retry)}</Button>}
    <Button variant="ghost" size="sm" onClick={onLatest}>{t(($) => $.message_list.jump_to_latest)}</Button>
  </div>;
}
