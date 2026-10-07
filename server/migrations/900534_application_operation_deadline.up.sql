ALTER TABLE application_operation ADD COLUMN IF NOT EXISTS deadline_at timestamptz;

UPDATE application_operation operation SET deadline_at=operation.created_at+interval '3 hours'+
 COALESCE((SELECT sum(
  600+COALESCE((SELECT sum((preparation->>'timeout_seconds')::integer)
   FROM jsonb_array_elements(COALESCE(NULLIF(step.command#>'{config,prepare}','null'::jsonb),'[]'::jsonb)) preparation),0)
  +CASE WHEN step.command#>>'{config,health,kind}'='none' THEN 0 ELSE COALESCE((step.command#>>'{config,health,timeout_seconds}')::integer,60) END
   *(1+CASE WHEN step.command#>>'{config,restart,enabled}'='true' THEN COALESCE((step.command#>>'{config,restart,max_attempts}')::integer,0) ELSE 0 END)
  +CASE WHEN step.command#>>'{config,restart,enabled}'='true' THEN COALESCE((step.command#>>'{config,restart,max_attempts}')::integer,0)*COALESCE((step.command#>>'{config,restart,delay_seconds}')::integer,5) ELSE 0 END
 ) FROM application_operation_step step WHERE step.operation_id=operation.id AND step.workspace_id=operation.workspace_id),0)*interval '1 second'
WHERE operation.deadline_at IS NULL;

ALTER TABLE application_operation ALTER COLUMN deadline_at SET DEFAULT (now()+interval '3 hours');
ALTER TABLE application_operation ALTER COLUMN deadline_at SET NOT NULL;
