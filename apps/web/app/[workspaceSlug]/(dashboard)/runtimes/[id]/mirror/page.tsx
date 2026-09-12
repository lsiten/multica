"use client";

import { use } from "react";
import { RuntimeMirrorPage } from "@multica/views/runtimes";

export default function RuntimeMirrorRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <RuntimeMirrorPage runtimeId={id} />;
}
