-- name: CreateEventConfig :one
-- Timestamps come from the domain factory. participation is null at creation.
INSERT INTO event_configs (event_id, participation, registration, scoreboard_visibility,
                           participants_visibility, preview_description,
                           preview_picture, created_at, updated_at, updated_by,
                           max_team_size, min_team_size, max_teams,
                           brand_color, accent_color, accent_light, accent_dark,
                           accent_live, theme_version, allow_pseudonyms,
                           stand_teardown_delay_minutes,
                           show_difficulty, hints_disabled,
                           results_freeze_enabled, results_freeze_minutes, results_opened_at,
                           results_live_freeze, results_chart_enabled, results_chart_teams,
                           results_rows_limit, hint_charge_mode,
                           show_start_countdown, show_finish_countdown, finish_countdown_minutes,
                           task_reveal_mode, max_flag_attempts, finish_countdown_mode)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
        $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24,
        $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37) RETURNING *;

-- name: GetEventConfig :one
SELECT *
FROM event_configs
WHERE event_id = $1;

-- name: UpdateEventConfig :execrows
-- Whole-aggregate write; excludes event_id/created_at (immutable). Optimistic
-- lock on updated_at (a fresh config from CreateEventConfig has updated_at set
-- to created_at, so the lock has a non-null baseline from the first write).
UPDATE event_configs
SET participation            = $2,
    registration             = $3,
    scoreboard_visibility    = $4,
    participants_visibility  = $5,
    preview_description      = $6,
    preview_picture          = $7,
    updated_at               = $8,
    updated_by               = $9,
    max_team_size            = $10,
    min_team_size            = $11,
    max_teams                = $12,
    brand_color             = $13,
    accent_color            = $14,
    accent_light            = $15,
    accent_dark             = $16,
    accent_live             = $17,
    theme_version           = $18,
    allow_pseudonyms        = $19,
    stand_teardown_delay_minutes = $20,
    show_difficulty              = $21,
    hints_disabled                   = $22,
    results_freeze_enabled       = $23,
    results_freeze_minutes       = $24,
    results_opened_at            = $25,
    results_live_freeze          = $26,
    results_chart_enabled        = $27,
    results_chart_teams          = $28,
    results_rows_limit           = $29,
    hint_charge_mode             = $30,
    show_start_countdown         = $31,
    show_finish_countdown        = $32,
    finish_countdown_minutes     = $33,
    task_reveal_mode             = $34,
    max_flag_attempts            = $35,
    finish_countdown_mode        = $36
WHERE event_id = $1
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: GetEventCapacityEstimate :one
-- The organizer's estimate of tasks that are not final yet. A narrow pair of queries: the
-- whole-aggregate write above never touches these columns.
SELECT capacity_expected_dynamic_tasks, capacity_avg_task_cpu_millicores, capacity_avg_task_memory_bytes
FROM event_configs
WHERE event_id = sqlc.arg(event_id);

-- name: SetEventCapacityEstimate :execrows
UPDATE event_configs
SET capacity_expected_dynamic_tasks  = sqlc.arg(expected_dynamic_tasks),
    capacity_avg_task_cpu_millicores = sqlc.arg(avg_task_cpu_millicores),
    capacity_avg_task_memory_bytes   = sqlc.arg(avg_task_memory_bytes)
WHERE event_id = sqlc.arg(event_id);
