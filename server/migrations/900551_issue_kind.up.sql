-- Requirement dimension (MUL-...).
--
-- A 'requirement' issue carries a requirement document (its description, edited
-- and confirmed through document_content_version) plus a confirmation lifecycle
-- (see the requirement table). It still sits on the 7-state board like any
-- issue; `kind` is the marker. 'task' is the existing default.
ALTER TABLE issue ADD COLUMN kind TEXT NOT NULL DEFAULT 'task'
    CHECK (kind IN ('task', 'requirement'));
