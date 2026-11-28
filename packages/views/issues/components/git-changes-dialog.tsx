"use client";

import { useState } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { GitChangesPanel, type GitChangesRequest, type GitChangesScope } from "./git-changes-panel";

export function GitChangesDialog({ request, initialScope, onClose }: {
  readonly request: GitChangesRequest;
  readonly initialScope?: GitChangesScope;
  readonly onClose: () => void;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(true);
  return <Dialog open={open} onOpenChange={setOpen} onOpenChangeComplete={(value) => { if (!value) onClose(); }}>
    <DialogContent className="flex max-h-[90vh] w-full max-w-6xl flex-col" aria-describedby={undefined}>
      <DialogHeader><DialogTitle>{t(($) => $.git_changes.title)}</DialogTitle></DialogHeader>
      <div className="min-h-0 overflow-y-auto"><GitChangesPanel request={request} initialScope={initialScope} /></div>
    </DialogContent>
  </Dialog>;
}
