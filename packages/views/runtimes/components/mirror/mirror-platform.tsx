"use client";
import { createContext, useContext, type ReactNode } from "react";
import type { VscreenScope } from "@multica/core/types";

export interface MirrorPlatform {
  readonly openFloating?: (scope: VscreenScope, title: string) => Promise<void>;
}
const MirrorPlatformContext = createContext<MirrorPlatform>({});
export function MirrorPlatformProvider({
  value,
  children,
}: {
  readonly value: MirrorPlatform;
  readonly children: ReactNode;
}) {
  return (
    <MirrorPlatformContext.Provider value={value}>
      {children}
    </MirrorPlatformContext.Provider>
  );
}
export function useMirrorPlatform(): MirrorPlatform {
  return useContext(MirrorPlatformContext);
}
