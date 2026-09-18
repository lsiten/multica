import type { VscreenScope } from "@multica/core/types";
export const RUNTIME_MIRROR_CHANNEL = "runtime-mirror:window";
export type RuntimeMirrorWindowContext = {
  readonly kind: "runtime-mirror";
  readonly scope: VscreenScope;
  readonly title: string;
  readonly generation: number;
};
export type RuntimeMirrorWindowRequest = {
  readonly scope: VscreenScope;
  readonly title: string;
};
const identity = (value: unknown): value is string =>
  typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(value);
export function parseRuntimeMirrorWindowRequest(
  value: unknown,
): RuntimeMirrorWindowRequest | null {
  if (
    !value ||
    typeof value !== "object" ||
    !("scope" in value) ||
    !("title" in value)
  )
    return null;
  const scope = value.scope;
  if (
    !scope ||
    typeof scope !== "object" ||
    typeof value.title !== "string" ||
    value.title.length > 256
  )
    return null;
  if (
    !("backendIdentity" in scope) ||
    !("accountId" in scope) ||
    !("workspaceId" in scope) ||
    !("runtimeId" in scope)
  )
    return null;
  if (
    typeof scope.backendIdentity !== "string" ||
    !identity(scope.accountId) ||
    !identity(scope.workspaceId) ||
    !identity(scope.runtimeId)
  )
    return null;
  try {
    const url = new URL(scope.backendIdentity);
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      scope.backendIdentity.endsWith("/")
    )
      return null;
    return {
      title: value.title,
      scope: {
        backendIdentity: scope.backendIdentity,
        accountId: scope.accountId,
        workspaceId: scope.workspaceId,
        runtimeId: scope.runtimeId,
      },
    };
  } catch (error) {
    if (error instanceof TypeError) return null;
    throw error;
  }
}
export function runtimeMirrorWindowKey(scope: VscreenScope): string {
  return JSON.stringify([
    scope.backendIdentity,
    scope.accountId,
    scope.workspaceId,
    scope.runtimeId,
  ]);
}

export const RUNTIME_MIRROR_ARGUMENT = "--multica-runtime-mirror=";
export function readRuntimeMirrorWindowContext(
  argv: readonly string[],
): RuntimeMirrorWindowContext | null {
  const argument = argv.find((value) =>
    value.startsWith(RUNTIME_MIRROR_ARGUMENT),
  );
  if (!argument) return null;
  try {
    const raw: unknown = JSON.parse(
      decodeURIComponent(argument.slice(RUNTIME_MIRROR_ARGUMENT.length)),
    );
    const request = parseRuntimeMirrorWindowRequest(raw);
    if (
      !request ||
      !raw ||
      typeof raw !== "object" ||
      !("generation" in raw) ||
      typeof raw.generation !== "number" ||
      !Number.isSafeInteger(raw.generation) ||
      raw.generation < 0
    )
      return null;
    return { ...request, kind: "runtime-mirror", generation: raw.generation };
  } catch (error) {
    if (error instanceof SyntaxError || error instanceof URIError) return null;
    throw error;
  }
}
