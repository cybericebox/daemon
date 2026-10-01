-- Archived exercises are hidden from the catalog and frozen for edits;
-- existing event attachments keep working. NULL = active.
ALTER TABLE exercises ADD COLUMN archived_at timestamptz;
