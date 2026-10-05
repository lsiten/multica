"use client";
import type { HumanResponseReceipt as Receipt } from "@multica/core/types/human-request";
import { useT } from "../i18n";
export function HumanResponseReceipt({ receipt }: { receipt?: Receipt | null }) {
 const { t } = useT("common");
 return receipt ? <p role="status" className="mt-2 break-words text-caption text-muted-foreground" data-human-response={receipt.request_id}>{t($ => $.human_request.reply_confirmed, { answer: receipt.label })}</p> : null;
}
