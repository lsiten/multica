CREATE TABLE IF NOT EXISTS execution_message_receipt (
 execution_id uuid NOT NULL, sequence integer NOT NULL, payload_hash text NOT NULL
);
