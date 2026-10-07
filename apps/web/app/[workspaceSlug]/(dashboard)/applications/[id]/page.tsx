"use client";

import { use } from "react";
import { ApplicationDetail } from "@multica/views/applications";

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  return <ApplicationDetail applicationId={id} />;
}
