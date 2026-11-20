import type { ApplicationConfig, ApplicationRelation } from "./schema";

type JsonValue = string | number | boolean | null | readonly JsonValue[] | { readonly [key: string]: JsonValue };

export interface ApplicationSnapshot {
  readonly config: ApplicationConfig;
  readonly relations: readonly ApplicationRelation[];
}

export interface ApplicationRevisionChange {
  readonly path: string;
  readonly before: string | null;
  readonly after: string | null;
  readonly sensitive: boolean;
}

function objectValue(value: JsonValue | undefined): value is { readonly [key: string]: JsonValue } {
  return value !== undefined && value !== null && typeof value === "object" && !Array.isArray(value);
}

function stableValue(value: JsonValue): string {
  if (Array.isArray(value)) return `[${value.map(stableValue).join(",")}]`;
  if (objectValue(value)) return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableValue(value[key] ?? null)}`).join(",")}}`;
  return JSON.stringify(value);
}

function comparableSnapshot(snapshot: ApplicationSnapshot) {
  return {
    ...snapshot.config,
    connections: [...snapshot.config.connections].sort((first, second) => `${first.target_id}:${first.url_variable}`.localeCompare(`${second.target_id}:${second.url_variable}`)),
    relations: snapshot.relations.map(({ target_id, type, required, condition, start_external }) => ({ target_id, type, required, condition, start_external })).sort((first, second) => `${first.type}:${first.target_id}`.localeCompare(`${second.type}:${second.target_id}`)),
  };
}

/** Diff values before redacting, so changed credentials never appear unchanged. */
export function applicationRevisionDiff(before: ApplicationSnapshot, after: ApplicationSnapshot): readonly ApplicationRevisionChange[] {
  const changes: ApplicationRevisionChange[] = [];
  const visit = (left: JsonValue | undefined, right: JsonValue | undefined, path: string) => {
    if (objectValue(left) && objectValue(right)) {
      for (const key of [...new Set([...Object.keys(left), ...Object.keys(right)])].sort()) visit(left[key], right[key], path ? `${path}.${key}` : key);
      return;
    }
    if (left === undefined && right === undefined || left !== undefined && right !== undefined && stableValue(left) === stableValue(right)) return;
    const sensitive = path.startsWith("environment.");
    const display = (value: JsonValue | undefined) => value === undefined ? null : sensitive ? "••••••" : typeof value === "string" ? value : stableValue(value);
    changes.push({ path, before: display(left), after: display(right), sensitive });
  };
  visit(comparableSnapshot(before), comparableSnapshot(after), "");
  return changes;
}
