"use client";

import { useState } from "react";
import { Check, Copy, KeyRound, Loader2 } from "@multica/ui/icons";
import { useCreateApplicationServiceAccess, type Application } from "@multica/core/applications";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { copyText } from "@multica/ui/lib/clipboard";
import { useLocale, useT } from "../i18n";

export function ApplicationAccessDialog({ workspaceId, application, endpointId }: { readonly workspaceId: string; readonly application: Application; readonly endpointId: string }) {
  const { t } = useT("applications");
  const locale = useLocale();
  const access = useCreateApplicationServiceAccess(workspaceId, application.id);
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<"url" | "token" | null>(null);
  const copy = (kind: "url" | "token", value: string) => { void copyText(value).then((ok) => { if (!ok) return; setCopied(kind); setTimeout(() => setCopied(null), 1500); }); };
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger render={<Button size="xs" variant="ghost"><KeyRound />{t(($) => $.create_access)}</Button>} /><DialogContent className="sm:max-w-lg"><DialogHeader><DialogTitle>{t(($) => $.service_access)}</DialogTitle></DialogHeader><div className="space-y-4">{!access.data && <p className="text-caption text-muted-foreground">{t(($) => $.service_access_help)}</p>}{access.error && <p role="alert" className="text-caption text-destructive">{access.error instanceof Error ? access.error.message : t(($) => $.load_failed)}</p>}{!access.data ? <Button onClick={() => void access.mutateAsync(endpointId)} disabled={access.isPending}>{access.isPending && <Loader2 className="animate-spin" />}{t(($) => $.create_access)}</Button> : <><label className="block space-y-1.5 text-caption font-medium">{t(($) => $.access_url)}<div className="flex gap-2"><Input value={access.data.url} readOnly className="font-mono text-caption" /><Button type="button" size="icon-sm" variant="outline" aria-label={t(($) => $.copy_access_url)} onClick={() => copy("url", access.data.url)}>{copied === "url" ? <Check className="text-success" /> : <Copy />}</Button></div></label><label className="block space-y-1.5 text-caption font-medium">{t(($) => $.access_token)}<div className="flex gap-2"><Input value={access.data.token} readOnly className="font-mono text-caption" /><Button type="button" size="icon-sm" variant="outline" aria-label={t(($) => $.copy_access_token)} onClick={() => copy("token", access.data.token)}>{copied === "token" ? <Check className="text-success" /> : <Copy />}</Button></div></label><p className="text-caption text-warning">{t(($) => $.access_expires)} {new Date(Date.now() + access.data.expires_in_seconds * 1000).toLocaleString(locale)}</p></>}</div><DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>{t(($) => $.close)}</Button></DialogFooter></DialogContent></Dialog>;
}
