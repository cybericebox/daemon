-- Optional snapshot label ("Зробити снапшот" note). Kept apart from
-- admin_note, which is working-copy content. Only explicit checkpoints and
-- imports write it; drafts, publications and restored working copies stay ''.
ALTER TABLE exercise_versions ADD COLUMN label text NOT NULL DEFAULT '';
