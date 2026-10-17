/**
 * Ask the desktop shell to open a local file with the OS-registered app.
 * Web renders have no local filesystem bridge and return false.
 */
export function canOpenLocalPath(): boolean {
  if (typeof window === "undefined") return false;
  const api = (window as unknown as { desktopAPI?: { openPath?: unknown } }).desktopAPI;
  return typeof api?.openPath === "function";
}

export async function openLocalPath(filePath: string): Promise<boolean> {
  if (typeof window === "undefined") return false;
  const api = (window as unknown as {
    desktopAPI?: { openPath?: (path: string) => Promise<{ ok?: boolean }> };
  }).desktopAPI;
  if (typeof api?.openPath !== "function") return false;
  const result = await api.openPath(filePath);
  return result?.ok === true;
}
