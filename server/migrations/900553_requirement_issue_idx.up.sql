-- One requirement per issue. Own single-statement file: CONCURRENTLY cannot run
-- inside a transaction and the runner executes files outside one (AGENTS.md).
CREATE UNIQUE INDEX CONCURRENTLY idx_requirement_issue ON requirement(issue_id);
