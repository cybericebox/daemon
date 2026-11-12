-- Copy of an event into a new one (docs/specs/ADMIN.md §2): settings, theme, pages, never people or results.

-- name: CopyEventSettings :exec
UPDATE events dst
SET scoring_mode             = src.scoring_mode,
    dynamic_algorithm        = src.dynamic_algorithm,
    dynamic_min_points       = src.dynamic_min_points,
    dynamic_max_points       = src.dynamic_max_points,
    dynamic_floor_at_percent = src.dynamic_floor_at_percent,
    force_event_scoring      = src.force_event_scoring,
    static_points            = src.static_points,
    landing_document         = src.landing_document,
    landing_draft            = src.landing_draft
FROM events src
WHERE dst.id = sqlc.arg(new_event_id)
  AND src.id = sqlc.arg(source_event_id);

-- name: CopyEventConfig :exec
UPDATE event_configs dst
SET participation                   = src.participation,
    registration                    = src.registration,
    scoreboard_visibility           = src.scoreboard_visibility,
    participants_visibility         = src.participants_visibility,
    preview_description             = src.preview_description,
    preview_picture                 = src.preview_picture,
    max_team_size                   = src.max_team_size,
    min_team_size                   = src.min_team_size,
    max_teams                       = src.max_teams,
    brand_color                     = src.brand_color,
    accent_color                    = src.accent_color,
    accent_light                    = src.accent_light,
    accent_dark                     = src.accent_dark,
    accent_live                     = src.accent_live,
    theme_version                   = src.theme_version,
    allow_pseudonyms                = src.allow_pseudonyms,
    stand_teardown_delay_minutes    = src.stand_teardown_delay_minutes,
    show_difficulty                 = src.show_difficulty,
    results_freeze_enabled          = src.results_freeze_enabled,
    results_freeze_minutes          = src.results_freeze_minutes,
    results_live_freeze             = src.results_live_freeze,
    results_chart_enabled           = src.results_chart_enabled,
    results_chart_teams             = src.results_chart_teams,
    results_rows_limit              = src.results_rows_limit,
    hint_charge_mode                = src.hint_charge_mode,
    hints_disabled                  = src.hints_disabled,
    show_start_countdown            = src.show_start_countdown,
    show_finish_countdown           = src.show_finish_countdown,
    finish_countdown_minutes        = src.finish_countdown_minutes,
    capacity_expected_dynamic_tasks = src.capacity_expected_dynamic_tasks,
    capacity_avg_task_cpu_millicores = src.capacity_avg_task_cpu_millicores,
    capacity_avg_task_memory_bytes  = src.capacity_avg_task_memory_bytes,
    task_reveal_mode                = src.task_reveal_mode,
    max_flag_attempts               = src.max_flag_attempts,
    finish_countdown_mode           = src.finish_countdown_mode,
    lab_policy                      = src.lab_policy,
    footer_signature                = src.footer_signature,
    results_freeze_end_mode         = src.results_freeze_end_mode,
    results_freeze_banner_text      = src.results_freeze_banner_text,
    results_freeze_banner_level     = src.results_freeze_banner_level
FROM event_configs src
WHERE dst.event_id = sqlc.arg(new_event_id)
  AND src.event_id = sqlc.arg(source_event_id);

-- name: CopyEventPages :exec
INSERT INTO event_pages (id, event_id, slug, title, document, visibility, navigation, navigation_order, created_at, updated_at, draft, published_at)
SELECT gen_random_uuid(), sqlc.arg(new_event_id), src.slug, src.title, src.document, src.visibility, src.navigation, src.navigation_order, sqlc.arg(now), sqlc.arg(now), src.draft, src.published_at
FROM event_pages src
WHERE src.event_id = sqlc.arg(source_event_id);
