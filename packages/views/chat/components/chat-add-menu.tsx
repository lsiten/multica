"use client";

import { useRef, useState } from "react";
import { Check, Clock3, DollarSign, FolderKanban, Image as ImageIcon, Plus, Settings2, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import type { ChatAutonomyPolicyOverride, Project } from "@multica/core/types";
import { ProjectIcon } from "../../projects/components/project-icon";
import { useT } from "../../i18n";

interface ChatAddMenuProps {
  /** Called with each selected file — the caller routes it through the
   *  editor's upload extension, same path as paste / drag-drop. */
  onSelectFile?: (file: File) => void;
  projects?: Project[];
  projectId?: string | null;
  onSelectProject?: (projectId: string | null) => void;
  /** Soft warning: the active agent's daemon is too old to receive the
   *  project description. Selection stays enabled; the submenu only appends
   *  an explanatory hint so the user knows before choosing. */
  projectContextUnsupported?: boolean;
  disabled?: boolean;
  autonomyPolicy?: ChatAutonomyPolicyOverride | null;
  onAutonomyPolicyChange?: (policy: ChatAutonomyPolicyOverride | null) => void;
}

/**
 * The "+" affordance at the bottom-left of the chat composer. Replaces the
 * standalone paperclip button: file upload now lives here as a submenu entry,
 * leaving room for future add-actions (agents, skills, tools) under one entry
 * point without crowding the input bar.
 */
export function ChatAddMenu({
  onSelectFile,
  projects = [],
  projectId,
  onSelectProject,
  projectContextUnsupported,
  disabled,
  autonomyPolicy,
  onAutonomyPolicyChange,
}: ChatAddMenuProps) {
  const { t } = useT("chat");
  const { t: tCommon } = useT("common");
  const inputRef = useRef<HTMLInputElement>(null);
  const [autonomyOpen, setAutonomyOpen] = useState(false);
  const [autonomyError, setAutonomyError] = useState(false);
  const [draft, setDraft] = useState({ duration: String(autonomyPolicy?.max_duration_seconds ?? 0), tokens: String(autonomyPolicy?.max_token_count ?? 0), cost: String(autonomyPolicy?.max_cost_usd_ticks ?? 0) });

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    if (files.length === 0) return;
    e.target.value = "";
    for (const file of files) onSelectFile?.(file);
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              disabled={disabled}
              aria-label={t(($) => $.input.add_tooltip)}
              title={t(($) => $.input.add_tooltip)}
              className="rounded-full text-muted-foreground"
            >
              <Plus />
            </Button>
          }
        />
        <DropdownMenuContent align="start" side="top" sideOffset={6}>
          {onSelectFile && (
            <DropdownMenuItem onClick={() => inputRef.current?.click()}>
              <ImageIcon />
              {t(($) => $.input.upload_file)}
            </DropdownMenuItem>
          )}
          {onSelectProject && (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <FolderKanban />
                {t(($) => $.input.project_context)}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="max-h-72 min-w-52 overflow-y-auto">
                {projects.map((project) => (
                  <DropdownMenuItem
                    key={project.id}
                    onClick={() => onSelectProject(project.id)}
                  >
                    <ProjectIcon project={project} size="md" />
                    <span className="min-w-0 flex-1 truncate">{project.title}</span>
                    {project.id === projectId && <Check className="ml-auto" />}
                  </DropdownMenuItem>
                ))}
                {projects.length === 0 && (
                  <div className="px-2 py-1.5 text-caption text-muted-foreground">
                    {t(($) => $.input.no_projects)}
                  </div>
                )}
                {projectId && <DropdownMenuSeparator />}
                {projectId && (
                  <DropdownMenuItem onClick={() => onSelectProject(null)}>
                    <X />
                    {t(($) => $.input.remove_project_context)}
                  </DropdownMenuItem>
                )}
                {projectContextUnsupported && (
                  <>
                    <DropdownMenuSeparator />
                    <div className="max-w-56 px-2 py-1.5 text-caption text-muted-foreground">
                      {t(($) => $.input.project_context_unsupported)}
                    </div>
                  </>
                )}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
          {onAutonomyPolicyChange && (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger><Settings2 />{t(($) => $.input.autonomy_mode)}</DropdownMenuSubTrigger>
              <DropdownMenuSubContent>
                <DropdownMenuItem onClick={() => onAutonomyPolicyChange(null)}>
                  {t(($) => $.input.autonomy_off)}{!autonomyPolicy && <Check className="ml-auto" />}
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => { setDraft({ duration: String(autonomyPolicy?.max_duration_seconds ?? 0), tokens: String(autonomyPolicy?.max_token_count ?? 0), cost: String(autonomyPolicy?.max_cost_usd_ticks ?? 0) }); setAutonomyError(false); setAutonomyOpen(true); }}>
                  <Settings2 />{t(($) => $.input.autonomy_configure)}{autonomyPolicy && <Check className="ml-auto" />}
                </DropdownMenuItem>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {onSelectFile && (
        <input
          ref={inputRef}
          type="file"
          multiple
          className="hidden"
          onChange={handleChange}
        />
      )}
      <Dialog open={autonomyOpen} onOpenChange={setAutonomyOpen}>
        <DialogContent className="max-w-md gap-4">
          <DialogHeader><DialogTitle>{t(($) => $.input.autonomy_mode)}</DialogTitle><DialogDescription>{t(($) => $.input.autonomy_hint)}</DialogDescription></DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5"><Label htmlFor="chat-autonomy-duration"><Clock3 className="mr-1 inline h-3.5 w-3.5" />{t(($) => $.input.autonomy_duration)}</Label><Input id="chat-autonomy-duration" inputMode="numeric" aria-invalid={autonomyError || undefined} value={draft.duration} onChange={(event) => { setAutonomyError(false); setDraft({ ...draft, duration: event.target.value }); }} /></div>
            <div className="space-y-1.5"><Label htmlFor="chat-autonomy-tokens">{t(($) => $.input.autonomy_tokens)}</Label><Input id="chat-autonomy-tokens" inputMode="numeric" aria-invalid={autonomyError || undefined} value={draft.tokens} onChange={(event) => { setAutonomyError(false); setDraft({ ...draft, tokens: event.target.value }); }} /></div>
            <div className="space-y-1.5"><Label htmlFor="chat-autonomy-cost"><DollarSign className="mr-1 inline h-3.5 w-3.5" />{t(($) => $.input.autonomy_cost)}</Label><Input id="chat-autonomy-cost" inputMode="numeric" aria-invalid={autonomyError || undefined} value={draft.cost} onChange={(event) => { setAutonomyError(false); setDraft({ ...draft, cost: event.target.value }); }} /></div>
            {autonomyError && <p role="alert" className="text-caption text-destructive">{t(($) => $.input.autonomy_invalid)}</p>}
            <p className="text-caption text-muted-foreground">{t(($) => $.input.autonomy_zero_unlimited)}</p>
          </div>
          <DialogFooter><Button variant="outline" onClick={() => setAutonomyOpen(false)}>{tCommon(($) => $.cancel)}</Button><Button onClick={() => { const duration = Number(draft.duration), tokens = Number(draft.tokens), cost = Number(draft.cost); if (!Number.isSafeInteger(duration) || !Number.isSafeInteger(tokens) || !Number.isSafeInteger(cost) || duration < 0 || duration > 86400 || tokens < 0 || cost < 0) { setAutonomyError(true); return; } onAutonomyPolicyChange?.({ mode: "autonomous", max_duration_seconds: duration, max_token_count: tokens, max_cost_usd_ticks: cost }); setAutonomyOpen(false); }}>{t(($) => $.input.autonomy_configure)}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
