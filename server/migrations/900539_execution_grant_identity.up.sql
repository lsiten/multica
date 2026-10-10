CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS execution_grant_identity_idx ON execution_grant (token_hash);
