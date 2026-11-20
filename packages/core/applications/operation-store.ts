import { create } from "zustand";

interface ApplicationOperationPanelState {
  readonly workspaceId: string | null;
  readonly focused: { readonly applicationId: string; readonly operationId: string } | null;
  readonly open: (workspaceId: string) => void;
  readonly focus: (workspaceId: string, applicationId: string, operationId: string) => void;
  readonly close: () => void;
}

/** Client-owned navigation only; operation payloads stay in Query. */
export const useApplicationOperationPanel = create<ApplicationOperationPanelState>((set) => ({
  workspaceId: null,
  focused: null,
  open: (workspaceId) => set({ workspaceId, focused: null }),
  focus: (workspaceId, applicationId, operationId) => set({ workspaceId, focused: { applicationId, operationId } }),
  close: () => set({ workspaceId: null, focused: null }),
}));
