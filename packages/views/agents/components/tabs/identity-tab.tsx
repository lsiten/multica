"use client";

import { useEffect, useState } from "react";
import { Loader2, Save } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Agent, AgentIdentity } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentIdentityOptions } from "@multica/core/agents/queries";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import { useT } from "../../../i18n";

type IdentityDraft = {
  email: string;
  phone: string;
};

function identityToDraft(identity: AgentIdentity): IdentityDraft {
  return {
    email: identity.email ?? "",
    phone: identity.phone ?? "",
  };
}

type IdentityTabProps = {
  agent: Agent;
  canEdit: boolean;
  onDirtyChange?: (dirty: boolean) => void;
};

export function IdentityTab(props: IdentityTabProps) {
  const wsId = useWorkspaceId();
  return <IdentityEditor key={`${wsId}:${props.agent.id}`} {...props} wsId={wsId} />;
}

function IdentityEditor({ agent, canEdit, onDirtyChange, wsId }: IdentityTabProps & { wsId: string }) {
  const { t } = useT("agents");
  const queryClient = useQueryClient();
  const options = agentIdentityOptions(wsId, agent.id);
  const query = useQuery(options);
  const [edited, setEdited] = useState<IdentityDraft | null>(null);
  const original = query.data ? identityToDraft(query.data) : null;
  const draft = edited ?? original;
  const dirty = edited !== null && JSON.stringify(edited) !== JSON.stringify(original);

  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);

  const mutation = useMutation({
    mutationFn: (value: IdentityDraft) => api.updateAgentIdentity(agent.id, {
      email: value.email.trim(),
      phone: value.phone.trim(),
    }),
    onMutate: () => queryClient.cancelQueries({ queryKey: options.queryKey }),
    onSuccess: async (identity) => {
      await queryClient.cancelQueries({ queryKey: options.queryKey });
      queryClient.setQueryData(options.queryKey, identity);
      setEdited(null);
      toast.success(t(($) => $.tab_body.identity.saved_toast));
    },
    onError: () => toast.error(t(($) => $.tab_body.identity.save_failed_toast)),
  });
  const saving = mutation.isPending;
  const loadError = query.isError && (
    <div className="space-y-2">
      <p role="alert" className="text-caption text-destructive">
        {t(($) => $.tab_body.identity.load_failed)}
      </p>
      <Button type="button" variant="outline" disabled={query.isFetching}
        onClick={() => void query.refetch()}>
        {t(($) => $.tab_body.identity.retry)}
      </Button>
    </div>
  );

  if (query.isPending) return <Skeleton className="h-56 w-full" />;
  if (!draft) return loadError;

  return (
    <div className="space-y-5">
      {loadError}
      <p className="text-caption text-muted-foreground">
        {t(($) => $.tab_body.identity.description)}
      </p>
      <div className="space-y-1.5">
        <Label htmlFor="agent-identity-email">{t(($) => $.tab_body.identity.email_label)}</Label>
        <Input
          id="agent-identity-email"
          value={draft.email}
          disabled={!canEdit || saving}
          onChange={(event) => setEdited({ ...draft, email: event.target.value })}
          placeholder={t(($) => $.tab_body.identity.email_placeholder)}
          type="email"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="agent-identity-phone">{t(($) => $.tab_body.identity.phone_label)}</Label>
        <Input
          id="agent-identity-phone"
          value={draft.phone}
          disabled={!canEdit || saving}
          onChange={(event) => setEdited({ ...draft, phone: event.target.value })}
          placeholder={t(($) => $.tab_body.identity.phone_placeholder)}
          inputMode="tel"
        />
      </div>
      {canEdit && (
        <Button type="button" aria-busy={saving}
          onClick={() => {
            if (canEdit && dirty && !saving && !query.isError) mutation.mutate(draft);
          }}
        disabled={!dirty || saving || query.isError}>
          {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
          {t(($) => $.tab_body.identity.save)}
        </Button>
      )}
    </div>
  );
}
