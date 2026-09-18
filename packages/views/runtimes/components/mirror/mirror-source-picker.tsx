import type {
  MirrorSourceBinding,
  VscreenSourceDescriptor,
} from "@multica/core/types";
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
  readonly catalog: readonly VscreenSourceDescriptor[];
  readonly selected: string;
  readonly enabled: boolean;
  readonly compact: boolean;
  readonly onSelect: (key: string) => void;
}) {
  const { t } = useT("runtimes");
  const kindLabel = (kind: MirrorSourceBinding["source"]["kind"]) => {
    switch (kind) {
      case "virtual":
        return t(($) => $.vscreen.virtual);
      case "physical":
        return t(($) => $.vscreen.physical);
      case "system":
        return t(($) => $.vscreen.system);
      default:
        return assertNever(kind);
    }
  };
  const sourceLabel = (entry: VscreenSourceDescriptor, index: number) =>
    `${kindLabel(entry.source.kind)} · ${entry.name.trim() || String(index + 1)}`;
  const selectedIndex = catalog.findIndex(
    (entry) => mirrorSourceKey(entry) === selected,
  );
  const selectedEntry = catalog[selectedIndex];
  return (
    <label className="flex min-w-0 flex-1 items-center gap-2 text-caption">
      {!compact && (
        <span className="shrink-0 text-muted-foreground">
          {t(($) => $.vscreen.source)}
        </span>
      )}
      <select
        aria-label={t(($) => $.vscreen.source)}
        title={
          selectedEntry
            ? sourceLabel(selectedEntry, selectedIndex)
            : selected
              ? t(($) => $.vscreen.source_gone)
              : t(($) => $.vscreen.choose_source)
        }
        value={selected}
        disabled={!enabled}
        onChange={(event) => onSelect(event.target.value)}
        className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 text-caption focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <option value="">{t(($) => $.vscreen.choose_source)}</option>
        {!!selected &&
          !catalog.some((entry) => mirrorSourceKey(entry) === selected) && (
            <option value={selected} disabled>
              {t(($) => $.vscreen.source_gone)}
            </option>
          )}
        {catalog.map((entry, index) => (
          <option
            title={sourceLabel(entry, index)}
            key={mirrorSourceKey(entry)}
            value={mirrorSourceKey(entry)}
          >
            {sourceLabel(entry, index)}
          </option>
        ))}
      </select>
    </label>
  );
}
