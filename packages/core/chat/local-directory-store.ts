import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";

/** The directory currently selected by the desktop file surface. */
export interface CurrentLocalDirectory {
  path: string;
  taskId: string;
  runtimeId: string;
  workspaceId: string;
  label?: string;
}

interface LocalDirectoryState {
  currentByWorkspace: Record<string, CurrentLocalDirectory | null>;
  setCurrentDirectory: (directory: CurrentLocalDirectory) => void;
  clearCurrentDirectory: (workspaceId: string) => void;
}

/**
 * Client-owned directory selection. It intentionally has no chat or project
 * key: Source Control follows the directory the user last opened, like VS
 * Code, while the task/runtime ids remain the daemon's security binding.
 */
export const useLocalDirectoryStore = create<LocalDirectoryState>()(
  persist(
    (set) => ({
      currentByWorkspace: {},
      setCurrentDirectory: (directory) =>
        set((state) => ({
          currentByWorkspace: {
            ...state.currentByWorkspace,
            [directory.workspaceId]: directory,
          },
        })),
      clearCurrentDirectory: (workspaceId) =>
        set((state) => ({
          currentByWorkspace: {
            ...state.currentByWorkspace,
            [workspaceId]: null,
          },
        })),
    }),
    {
      name: "multica_local_directory",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (state) => ({ currentByWorkspace: state.currentByWorkspace }),
      version: 1,
    },
  ),
);

export function selectCurrentLocalDirectory(workspaceId: string | null) {
  return (state: LocalDirectoryState) =>
    workspaceId ? state.currentByWorkspace[workspaceId] ?? null : null;
}
