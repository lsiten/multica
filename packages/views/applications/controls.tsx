"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";

export function ApplicationSelect({ value, onChange, items, label, disabled }: { value: string; onChange: (value: string) => void; items: { value: string; label: string }[]; label: string; disabled?: boolean }) {
  return <Select value={value || null} onValueChange={(next) => onChange(next ?? "")} items={items} disabled={disabled}>
    <SelectTrigger aria-label={label} className="w-full min-w-0"><SelectValue placeholder={label} /></SelectTrigger>
    <SelectContent>{items.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent>
  </Select>;
}

export function ApplicationStatusBadge({ status }: { status: string }) {
  const { t } = useT("applications");
  const labels = {
    starting: t(($) => $.status.starting), running: t(($) => $.status.running), stopping: t(($) => $.status.stopping), stopped: t(($) => $.status.stopped),
    failed: t(($) => $.status.failed), unhealthy: t(($) => $.status.unhealthy), offline: t(($) => $.status.offline), unknown: t(($) => $.status.unknown),
    healthy: t(($) => $.status.healthy), checking: t(($) => $.status.checking), none: t(($) => $.status.none), online: t(($) => $.status.online),
    queued: t(($) => $.status.queued), cancelling: t(($) => $.status.cancelling), completed: t(($) => $.status.completed), partial: t(($) => $.status.partial), cancelled: t(($) => $.status.cancelled), blocked: t(($) => $.status.blocked), preparing: t(($) => $.status.preparing),
  };
  const label = Object.hasOwn(labels, status) ? labels[status as keyof typeof labels] : labels.unknown;
  return <Badge variant="outline" className={cn("gap-1.5 whitespace-nowrap text-caption", (status === "running" || status === "healthy" || status === "completed") && "border-success/30 text-success", (status === "failed" || status === "unhealthy") && "border-destructive/30 text-destructive", (status === "partial" || status === "offline" || status === "blocked") && "border-warning/30 text-warning")}>
    <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-current" />{label}
  </Badge>;
}
