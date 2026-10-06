alter table exercises
    drop column if exists draft_version_id,
    drop column if exists published_version_id;
drop table if exists exercise_versions;
drop table if exists exercises;
