CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS application_service_grant_hash_idx ON application_service_grant (token_hash);
