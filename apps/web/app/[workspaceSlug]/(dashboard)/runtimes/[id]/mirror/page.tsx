"use client";

import { use } from "react";
import { MirrorPlatformProvider, RuntimeMirrorPage } from "@multica/views/runtimes";

export default function RuntimeMirrorRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <MirrorPlatformProvider
      value={{
        openFloating: async () => {
          const presentation = window.open(window.location.href, "_blank");
          if (!presentation) throw new Error("Presentation view was blocked");
          presentation.opener = null;
        },
      }}
    >
      <RuntimeMirrorPage runtimeId={id} />
    </MirrorPlatformProvider>
  );
}
