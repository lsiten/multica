import type {
  MirrorSourceDescriptor,
  MirrorSourceBinding,
} from "@multica/core/types";
import { Check, ChevronDown, Monitor } from "@multica/ui/icons";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useT } from "../../../i18n";
import { assertNever } from "./assert-never";

export function mirrorSourceKey(binding: MirrorSourceBinding): string {
  return JSON.stringify([
    binding.source.kind,
    binding.source.sourceId,
    binding.nativeEpoch,
    binding.generation,
  ]);
}
export function MirrorSourcePicker({
  catalog,
  selected,
  enabled,
  compact,
  onSelect,
}: {
  readonly catalog: readonly MirrorSourceDescriptor[];
  readonly selected: string;
  readonly enabled: boolean;
  readonly compact: boolean;
  readonly onSelect: (key: string) => void;
}) {
  const { t } = useT("runtimes");
  const kindLabel = (kind: MirrorSourceBinding["source"]["kind"]) => {
    switch (kind) {
      case "physical":
        return t(($) => $.vscreen.physical);
      case "system":
        return t(($) => $.vscreen.system);
      default:
        return assertNever(kind);
    }
  };
  const sourceLabel = (entry: MirrorSourceDescriptor, index: number) =>
    `${kindLabel(entry.source.kind)} · ${entry.name.trim() || String(index + 1)}`;
  const selectedIndex = catalog.findIndex(
    (entry) => mirrorSourceKey(entry) === selected,
  );
  const selectedEntry = catalog[selectedIndex];
  const selectedLabel = selectedEntry
    ? sourceLabel(selectedEntry, selectedIndex)
    : selected
      ? t(($) => $.vscreen.source_gone)
      : t(($) => $.vscreen.choose_source);
  return (
    <Popover>
      <PopoverTrigger
        disabled={!enabled}
        data-compact={compact ? "true" : undefined}
        aria-label={t(($) => $.vscreen.source)}
        title={selectedLabel}
        className="flex h-8 min-w-0 max-w-72 flex-1 items-center gap-2 rounded-md border bg-background px-2 text-left text-caption transition-colors hover:bg-accent disabled:pointer-events-none disabled:opacity-50 data-[popup-open]:bg-accent"
      >
        <Monitor className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate">{selectedLabel}</span>
        <ChevronDown className="ml-auto size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-2">
        <div className="px-2 pb-1 pt-1">
          <p className="text-caption font-semibold">{t(($) => $.vscreen.source)}</p>
          <p className="text-caption text-muted-foreground">{t(($) => $.vscreen.source_picker_hint)}</p>
        </div>
        <div className="max-h-80 space-y-1 overflow-y-auto" role="listbox" aria-label={t(($) => $.vscreen.source)}>
          {catalog.map((entry, index) => {
            const key = mirrorSourceKey(entry);
            const current = key === selected;
            return (
              <button
                key={key}
                type="button"
                role="option"
                aria-selected={current}
                title={sourceLabel(entry, index)}
                className={`flex w-full items-start gap-2 rounded-md border px-2 py-2 text-left transition-colors ${current ? "border-primary/40 bg-accent" : "border-transparent hover:bg-accent/60"}`}
                onClick={() => onSelect(key)}
              >
                <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-sm border bg-background">
                  <Monitor className="size-4 text-muted-foreground" aria-hidden="true" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5">
                    <span className="truncate text-caption font-medium">{entry.name.trim() || String(index + 1)}</span>
                    {entry.primary && <span className="rounded-sm bg-primary/10 px-1 py-0.5 text-micro text-primary">{t(($) => $.vscreen.primary_display)}</span>}
                  </span>
                  <span className="block truncate text-caption text-muted-foreground">
                    {kindLabel(entry.source.kind)} · {entry.width} × {entry.height} · {Math.round(entry.scale * 100)}%
                  </span>
                </span>
                {current && <Check className="mt-1 size-4 shrink-0 text-primary" aria-hidden="true" />}
              </button>
            );
          })}
          {!catalog.length && (
            <p className="px-2 py-4 text-caption text-muted-foreground">{t(($) => $.vscreen.empty)}</p>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
