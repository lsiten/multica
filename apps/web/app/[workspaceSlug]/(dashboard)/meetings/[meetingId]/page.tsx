"use client";

import { use } from "react";
import { MeetingDetailPage } from "@multica/views/meetings";

export default function Page({ params }: { params: Promise<{ meetingId: string }> }) {
  const { meetingId } = use(params);
  return <MeetingDetailPage meetingId={meetingId} />;
}
