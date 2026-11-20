-- name: ListAgentAnswerChunks :many
SELECT id, seq, type,
       substr(COALESCE(content, ''),
           (CASE WHEN seq = sqlc.arg('cursor_seq')::bigint
                 THEN sqlc.arg('content_offset')::int ELSE 0 END) + 1,
           4096)::text AS content,
       char_length(COALESCE(content, ''))::int AS total_chars,
       created_at
FROM task_message
WHERE task_id = @task_id
  AND seq >= @cursor_seq::bigint
  AND type IN ('text', 'error')
ORDER BY seq ASC
LIMIT @page_limit;
