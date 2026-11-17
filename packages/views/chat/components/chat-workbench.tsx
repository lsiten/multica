"use client";

import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useGroupRef } from "react-resizable-panels";
import { Columns2, Folder, Maximize2, RotateCcw, X } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@multica/ui/components/ui/resizable";
import { Sheet, SheetClose, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@multica/ui/components/ui/sheet";
import { cn } from "@multica/ui/lib/utils";
import { useChatWorkspaceLayoutStore, DEFAULT_CHAT_WORKSPACE_LAYOUT } from "@multica/core/chat";
import { useT } from "../../i18n";
import { ChatOverlayHeader } from "./chat-overlay-header";
import "./chat-workbench.css";

export function ChatWorkbench({ scope, documentKey, selectedDocument: _selectedDocument, conversation, document, files, tabs, conversationTitle, actions }: {
  readonly scope: string;
  readonly documentKey: string | null;
  readonly selectedDocument?: import("@multica/core/chat").DocumentRef | null;
  readonly conversation: ReactNode;
  readonly document: ReactNode;
  readonly files: ReactNode;
  readonly tabs?: ReactNode;
  readonly conversationTitle?: string;
  readonly actions?: ReactNode;
}) {
  const { t } = useT("chat");
  const root = useRef<HTMLDivElement>(null);
  const group = useGroupRef();
  const [wide, setWide] = useState(false);
  const [fileScope, setFileScope] = useState<string | null>(null);
  const selectionKey = JSON.stringify([scope, documentKey]);
  const [selection, setSelection] = useState({ key: "", pane: "document" });
  const selectedPane = selection.key === selectionKey ? selection.pane : "document";
  const layout = useChatWorkspaceLayoutStore((state) => state.layouts[scope] ?? DEFAULT_CHAT_WORKSPACE_LAYOUT);
  const setLayout = useChatWorkspaceLayoutStore((state) => state.setLayout);
  const parallel = !!documentKey && wide && layout.mode === "parallel";
  const floating = !!documentKey && wide && layout.mode === "focused" && selectedPane === "document";
  const showConversation = !documentKey || parallel || floating || selectedPane === "conversation";
  const showDocument = !!documentKey && (parallel || selectedPane === "document");
  const floatingStyle: CSSProperties & { "--chat-overlay-x"?: string; "--chat-overlay-y"?: string } = layout.overlay
    ? { "--chat-overlay-x": `${layout.overlay.x}px`, "--chat-overlay-y": `${layout.overlay.y}px` }
    : {};

  useEffect(() => {
    const element = root.current;
    if (!element) return;
    const body = element.querySelector<HTMLElement>("[data-chat-workbench-body]");
    const measure = () => {
      setWide(element.getBoundingClientRect().width >= 896);
      if (body) element.style.setProperty("--chat-workbench-height", `${body.getBoundingClientRect().height}px`);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    if (body) observer.observe(body);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    setFileScope(null);
  }, [documentKey, scope]);

  useEffect(() => {
    if (parallel) {
      group.current?.setLayout({ conversation: layout.conversationPercent, document: 100 - layout.conversationPercent });
    }
  }, [group, parallel, layout.conversationPercent, scope]);

  return (
    <div ref={root} data-slot="chat-workbench" data-floating={floating} data-overlay-collapsed={layout.overlay?.collapsed ?? false}
      onPointerDownCapture={(event) => {
        if (!floating || !(event.target instanceof Element) || !event.target.closest("[data-chat-transcript] button, [data-chat-transcript] a")) return;
        if (root.current?.ownerDocument.activeElement?.closest("[data-chat-composer-editor]")) event.preventDefault();
      }}
      style={floatingStyle} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-1 border-b px-3 py-1">
        {actions}
        <Sheet open={fileScope === scope} onOpenChange={(open) => setFileScope(open ? scope : null)}>
          <SheetTrigger render={<Button variant="ghost" size="sm" />}>
            <Folder aria-hidden="true" />{t(($) => $.doc_file_list.toggle)}
          </SheetTrigger>
          <SheetContent side="right" className="gap-0" showCloseButton={false}>
            <SheetHeader className="flex-row items-center justify-between border-b p-3">
              <SheetTitle>{t(($) => $.doc_file_list.toggle)}</SheetTitle>
              <SheetClose render={<IconButton label={t(($) => $.doc_mode.close)} />}><X /></SheetClose>
            </SheetHeader>
            <div className="flex min-h-0 flex-1 flex-col">{files}</div>
          </SheetContent>
        </Sheet>
        {tabs}
        {!!documentKey && (
          <>
            {!tabs && !parallel && !floating && (
              <div role="group" aria-label={t(($) => $.workspace.panes)} className="flex min-w-0 gap-1">
                <Button size="sm" variant={showConversation ? "secondary" : "ghost"} aria-pressed={showConversation}
                  onClick={() => setSelection({ key: selectionKey, pane: "conversation" })}>
                  {t(($) => $.workspace.conversation)}
                </Button>
                <Button size="sm" variant={showDocument ? "secondary" : "ghost"} aria-pressed={showDocument}
                  onClick={() => setSelection({ key: selectionKey, pane: "document" })}>
                  {t(($) => $.workspace.document)}
                </Button>
              </div>
            )}
            <div className="ml-auto flex items-center gap-1">
              {wide && (
                <IconButton label={t(($) => layout.mode === "focused" ? $.workspace.exit_full_tab : $.workspace.focus_document)}
                  onClick={() => {
                    setLayout(scope, { mode: layout.mode === "focused" ? "parallel" : "focused" });
                    setSelection({ key: selectionKey, pane: "document" });
                  }}>
                  {layout.mode === "focused" ? <Columns2 /> : <Maximize2 />}
                </IconButton>
              )}
              {parallel && (
                <IconButton label={t(($) => $.workspace.reset_layout)} onClick={() => setLayout(scope, DEFAULT_CHAT_WORKSPACE_LAYOUT)}>
                  <RotateCcw />
                </IconButton>
              )}
            </div>
          </>
        )}
      </div>
      <ResizablePanelGroup data-chat-workbench-body orientation="horizontal" groupRef={group} disabled={!parallel} className="relative min-h-0 flex-1"
        onLayoutChanged={(sizes) => {
          if (parallel && sizes.conversation !== undefined) setLayout(scope, { conversationPercent: sizes.conversation });
        }}>
        <ResizablePanel id="conversation" data-chat-pane="conversation" minSize="30%" defaultSize="45%"
          className={cn("min-h-0 min-w-0", !showConversation && "hidden!")}
          hidden={!showConversation}
          inert={!showConversation}>
          <section aria-label={t(($) => $.workspace.conversation)} className="flex h-full min-h-0 min-w-0 flex-col @container">
            <ChatOverlayHeader scope={scope} container={root} visible={floating} collapsed={layout.overlay?.collapsed ?? false}
              title={conversationTitle ?? t(($) => $.workspace.conversation)} />
            <div className="flex min-h-0 flex-1 flex-col" hidden={floating && layout.overlay?.collapsed}>
              {conversation}
            </div>
          </section>
        </ResizablePanel>
        <ResizableHandle aria-label={t(($) => $.workspace.resize_panels)}
          hidden={!parallel}
          className={cn("z-10 focus-visible:ring-2 focus-visible:ring-ring", !parallel && "hidden")} />
        <ResizablePanel id="document" minSize="30%" defaultSize="55%"
          className={cn("min-h-0 min-w-0", parallel && "border-l", !showDocument && "hidden!")}
          hidden={!showDocument}
          inert={!showDocument}>
          <section aria-label={t(($) => $.workspace.document)} className="flex h-full min-h-0 min-w-0 flex-col overflow-hidden">
            {document}
          </section>
        </ResizablePanel>
      </ResizablePanelGroup>
    </div>
  );
}
