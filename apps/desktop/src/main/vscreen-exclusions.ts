export function ownedMirrorWindowIDs(sources: readonly string[]): number[] {
  const ids = sources.map((source) => /^window:(\d+):\d+$/.exec(source)?.[1]).filter((id): id is string => !!id).map(Number);
  if (ids.some((id) => !Number.isSafeInteger(id) || id <= 0 || id > 0xffffffff) || ids.length > 32) throw new Error("invalid_window_registry");
  return [...new Set(ids)].sort((a, b) => a - b);
}
