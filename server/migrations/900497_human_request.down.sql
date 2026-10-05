ALTER TABLE chat_message DROP COLUMN IF EXISTS human_request_id;
ALTER TABLE comment DROP COLUMN IF EXISTS human_request_id;
DROP TABLE IF EXISTS human_request;
