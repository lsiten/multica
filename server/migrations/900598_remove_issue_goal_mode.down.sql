CREATE TABLE issue_goal (
    issue_id UUID PRIMARY KEY,
    objective TEXT NOT NULL CHECK (length(btrim(objective)) > 0),
    completed_at TIMESTAMPTZ,
    completed_by_type TEXT CHECK (completed_by_type IN ('member', 'agent')),
    completed_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((completed_at IS NULL AND completed_by_type IS NULL AND completed_by_id IS NULL)
        OR (completed_at IS NOT NULL AND completed_by_type IS NOT NULL AND completed_by_id IS NOT NULL))
);

CREATE OR REPLACE FUNCTION enforce_issue_goal_review_gate()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'in_review' AND EXISTS (
        SELECT 1 FROM issue_goal
        WHERE issue_id = NEW.id AND completed_at IS NULL
    ) THEN
        RAISE EXCEPTION 'goal-mode issue must be completed by its assignee before review';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_goal_review_gate
BEFORE UPDATE OF status ON issue
FOR EACH ROW EXECUTE FUNCTION enforce_issue_goal_review_gate();

CREATE OR REPLACE FUNCTION enforce_issue_goal_configuration_gate()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.completed_at IS NULL AND EXISTS (
        SELECT 1 FROM issue
        WHERE id = NEW.issue_id AND status = 'in_review'
    ) THEN
        RAISE EXCEPTION 'cannot enable an incomplete goal while the issue is in review';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_goal_configuration_gate
BEFORE INSERT OR UPDATE OF completed_at ON issue_goal
FOR EACH ROW EXECUTE FUNCTION enforce_issue_goal_configuration_gate();

CREATE OR REPLACE FUNCTION reset_issue_goal_completion_on_assignee_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.assignee_type IS DISTINCT FROM NEW.assignee_type
       OR OLD.assignee_id IS DISTINCT FROM NEW.assignee_id THEN
        UPDATE issue_goal
        SET completed_at = NULL,
            completed_by_type = NULL,
            completed_by_id = NULL,
            updated_at = now()
        WHERE issue_id = NEW.id AND completed_at IS NOT NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_goal_assignee_change
AFTER UPDATE OF assignee_type, assignee_id ON issue
FOR EACH ROW EXECUTE FUNCTION reset_issue_goal_completion_on_assignee_change();
