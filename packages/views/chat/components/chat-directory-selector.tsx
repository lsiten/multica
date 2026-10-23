import { useState } from "react";
import { FolderOpen } from "lucide-react";
import type { ChatFileDirectory } from "@multica/core/chat";
import { Button } from "@multica/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectLabel,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";

export function ChatDirectorySelector({ directories, selected, onSelect }: {
  readonly directories: readonly ChatFileDirectory[];
  readonly selected: ChatFileDirectory;
  readonly onSelect: (value: string | null) => void;
}) {
  const { t } = useT("chat");
  const [otherLimit, setOtherLimit] = useState(0);
  const others = directories.filter((directory) => directory.scope === "project_task");
  const visibleOtherKeys = new Set(others.slice(0, otherLimit).map((directory) => directory.key));
  const visible = directories.filter((directory) =>
    directory.scope !== "project_task" || directory.key === selected.key || visibleOtherKeys.has(directory.key),
  );
  const groups = [
    { scope: "project", label: t(($) => $.doc_file_list.project_directory) },
    { scope: "current_chat", label: t(($) => $.doc_file_list.chat_directories) },
    { scope: "project_task", label: t(($) => $.doc_file_list.other_directories) },
  ];

  return (
    <>
      <Select
        items={visible.map((directory) => ({
          value: directory.key,
          label: directory.label || directory.branchName || directory.path,
        }))}
        value={selected.key}
        onValueChange={onSelect}
      >
        <SelectTrigger
          className="w-full min-w-0"
          aria-label={t(($) => $.doc_file_list.directory)}
          title={selected.path}
        >
          <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <SelectValue className="min-w-0 truncate" />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false} align="start">
          {groups.map((group) => {
            const entries = visible.filter((directory) => directory.scope === group.scope);
            if (!entries.length) return null;
            return (
              <SelectGroup key={group.scope}>
                <SelectLabel>{group.label}</SelectLabel>
                {entries.map((directory) => (
                  <SelectItem key={directory.key} value={directory.key} className="[&_[data-slot]]:min-w-0">
                    <span className="flex min-w-0 flex-col items-start whitespace-normal text-left">
                      <span className="max-w-full break-words font-medium">
                        {directory.label || directory.branchName || t(($) => $.doc_file_list.run_directory)}
                      </span>
                      <span className="max-w-full break-all font-mono text-caption text-muted-foreground" dir="ltr">
                        {directory.path}
                      </span>
                    </span>
                  </SelectItem>
                ))}
              </SelectGroup>
            );
          })}
        </SelectContent>
      </Select>
      {others.length > 0 && (
        <div className="flex flex-wrap gap-1">
          <Button
            type="button"
            variant="ghost"
            size="xs"
            aria-expanded={otherLimit > 0}
            onClick={() => setOtherLimit(otherLimit > 0 ? 0 : 20)}
          >
            {t(($) => $.doc_file_list.other_directories)} ({others.length})
          </Button>
          {otherLimit > 0 && otherLimit < others.length && (
            <Button type="button" variant="ghost" size="xs" onClick={() => setOtherLimit((limit) => limit + 20)}>
              {t(($) => $.doc_file_list.load_more)}
            </Button>
          )}
        </div>
      )}
    </>
  );
}
