DROP TRIGGER IF EXISTS issue_goal_assignee_change ON issue;
DROP FUNCTION IF EXISTS reset_issue_goal_completion_on_assignee_change();
DROP TRIGGER IF EXISTS issue_goal_configuration_gate ON issue_goal;
DROP FUNCTION IF EXISTS enforce_issue_goal_configuration_gate();
DROP TRIGGER IF EXISTS issue_goal_review_gate ON issue;
DROP FUNCTION IF EXISTS enforce_issue_goal_review_gate();
DROP TABLE IF EXISTS issue_goal;
