DELETE FROM notification_in_app_templates
WHERE notification_type IN ('event.application.submitted', 'exercise.proposal.submitted',
                            'exercise.proposal.approved', 'exercise.proposal.rejected');

DROP INDEX IF EXISTS in_app_notifications_subject_ref_idx;
DROP INDEX IF EXISTS in_app_notifications_open_request_idx;
DROP INDEX IF EXISTS in_app_notifications_user_category_idx;

ALTER TABLE in_app_notifications
    DROP CONSTRAINT IF EXISTS in_app_notifications_resolved_check,
    DROP CONSTRAINT IF EXISTS in_app_notifications_action_category_check,
    DROP COLUMN resolved_by,
    DROP COLUMN resolution,
    DROP COLUMN resolved_at,
    DROP COLUMN subject_ref,
    DROP COLUMN action_required,
    DROP COLUMN category,
    DROP COLUMN notification_type;
