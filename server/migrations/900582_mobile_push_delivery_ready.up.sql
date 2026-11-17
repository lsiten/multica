CREATE INDEX CONCURRENTLY mobile_push_delivery_ready ON mobile_push_delivery(next_attempt_at) WHERE completed_at IS NULL;
