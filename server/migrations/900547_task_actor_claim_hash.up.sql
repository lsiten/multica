CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS task_actor_claim_hash ON task_actor_claim(token_hash);
