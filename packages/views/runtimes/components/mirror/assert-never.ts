export function assertNever(value: never): never {
  throw new Error(`Unexpected runtime mirror state: ${String(value)}`);
}
