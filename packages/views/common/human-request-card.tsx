"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  humanRequestKeys,
  humanRequestOptions,
  useHumanRequestExpiry,
  useHumanRequestRealtime,
  useRespondHumanRequest,
} from "@multica/core/human-requests";
import type { HumanRequest, HumanRequestAnswer } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";

export function HumanRequestCard({ requestId, fallbackContent }: {
  requestId: string;
  fallbackContent?: string;
}) {
  const workspaceId = useWorkspaceId();
  const query = useQuery(humanRequestOptions(workspaceId, requestId));
  useHumanRequestRealtime(workspaceId);
  useHumanRequestExpiry(workspaceId, query.data);
  const { t } = useT("common");

  if (!query.data) {
    return (
      <div className="space-y-2" aria-busy={query.isPending}>
        {fallbackContent && (
          <p className="whitespace-pre-wrap break-words text-body">{fallbackContent}</p>
        )}
        <p role={query.isError ? "alert" : "status"} className="text-caption text-muted-foreground">
          {query.isError ? t($ => $.human_request.load_failed) : t($ => $.human_request.loading)}
        </p>
        {query.isError && (
          <Button size="sm" variant="outline" onClick={() => void query.refetch()}>
            {t($ => $.human_request.retry)}
          </Button>
        )}
      </div>
    );
  }

  return (
    <HumanRequestContent
      key={`${query.data.id}:${query.data.revision}`}
      request={query.data}
      workspaceId={workspaceId}
    />
  );
}

export function HumanRequestContent({ request, workspaceId }: {
  request: HumanRequest;
  workspaceId: string;
}) {
  const { t } = useT("common");
  const [answer, setAnswer] = useState("");
  const submit = useRespondHumanRequest(workspaceId, request.id);
  const client = useQueryClient();
  const payload = request.payload;
  const pending = request.status === "pending" && Date.parse(request.expires_at) > Date.now();
  const actionable = pending && request.can_respond && payload.kind !== "unknown";
  const send = (decision: HumanRequestAnswer["decision"], value?: string) => {
    submit.mutate({ revision: request.revision, decision, ...(value ? { answer: value } : {}) });
  };
  const refresh = () => {
    submit.reset();
    void client.invalidateQueries({ queryKey: humanRequestKeys.detail(workspaceId, request.id) });
  };

  let status = t($ => $.human_request.unavailable);
  switch (request.status) {
    case "answered": status = t($ => $.human_request.received); break;
    case "declined": status = t($ => $.human_request.declined); break;
    case "cancelled": status = t($ => $.human_request.cancelled); break;
    case "expired": status = t($ => $.human_request.expired); break;
    case "pending": if (!pending) status = t($ => $.human_request.expired); break;
  }

  return (
    <section
      className="space-y-3 rounded-lg border bg-card p-4 text-body"
      aria-label={payload.title}
      data-human-request-id={request.id}
    >
      <h3 className="break-words font-medium">{payload.title}</h3>
      {pending && (
        <>
          {payload.steps.length > 0 && (
            <ol className="list-decimal space-y-1 pl-5">
              {payload.steps.map((step, index) => (
                <li key={index} className="whitespace-pre-wrap break-words">{step}</li>
              ))}
            </ol>
          )}
          {payload.impact && <p className="whitespace-pre-wrap break-words">{payload.impact}</p>}
          <p className="break-words text-caption text-muted-foreground">{payload.next}</p>
        </>
      )}
      {payload.details && (
        <details>
          <summary className="cursor-pointer text-caption text-muted-foreground">
            {t($ => $.human_request.details)}
          </summary>
          <p className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words text-caption">
            {payload.details}
          </p>
        </details>
      )}
      {!pending && <p role="status" className="text-caption text-muted-foreground">{status}</p>}
      {request.status === "answered" && request.response?.answer && <p className="break-words text-caption">{t($ => $.human_request.reply_confirmed, { answer: payload.choices.find(choice => choice.id === request.response?.answer)?.label ?? request.response.answer })}</p>}
      {pending && !actionable && (
        <p className="text-caption text-muted-foreground">
          {payload.kind === "unknown" ? t($ => $.human_request.unavailable) : t($ => $.human_request.other_member)}
        </p>
      )}
      {actionable && (
        <div className="space-y-3">
          {payload.kind === "input" && (
            <div className="space-y-1">
              <label htmlFor={`human-input-${request.id}`}>{payload.input_label}</label>
              <Textarea
                id={`human-input-${request.id}`}
                value={answer}
                onChange={event => setAnswer(event.target.value)}
                disabled={submit.isPending}
                maxLength={2000}
              />
            </div>
          )}
          <div className="flex flex-wrap gap-2">
            {payload.kind === "choice" ? payload.choices.map((choice, index) => (
              <Button
                key={choice.id}
                variant={choice.recommended ? "default" : "outline"}
                disabled={submit.isPending}
                aria-busy={submit.isPending}
                onClick={() => send("choice", choice.id)}
                className="h-auto min-h-[var(--button-height-default)] whitespace-normal text-left"
              >
                <span aria-hidden="true">{String.fromCharCode(65 + index)} / {index + 1}. </span>{choice.label}
                {choice.recommended && (
                  <span className="text-caption"> · {t($ => $.human_request.recommended)}</span>
                )}
              </Button>
            )) : (
              <Button
                disabled={submit.isPending || payload.kind === "input" && !answer.trim()}
                aria-busy={submit.isPending}
                onClick={() => send(
                  payload.kind === "manual" ? "completed" : payload.kind === "input" ? "input" : "approve",
                  payload.kind === "input" ? answer : undefined,
                )}
              >
                {payload.action_label}
              </Button>
            )}
            <Button variant="outline" disabled={submit.isPending} onClick={() => send("reject")}>
              {t($ => $.human_request.decline)}
            </Button>
          </div>
          {submit.isPending && (
            <p role="status" className="text-caption text-muted-foreground">
              {t($ => $.human_request.submitting)}
            </p>
          )}
          {submit.isError && (
            <div className="space-y-2">
              <p role="alert" className="text-caption text-destructive">{t($ => $.human_request.submit_failed)}</p>
              <Button variant="outline" size="sm" onClick={refresh}>{t($ => $.human_request.refresh)}</Button>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
