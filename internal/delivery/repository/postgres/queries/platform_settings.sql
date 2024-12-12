-- name: GetPlatformSettings :one
select value
from platform_settings
where key = $1;

-- name: UpdatePlatformSettings :execrows
update platform_settings
set value      = $2,
    updated_at = now(),
    updated_by = $3
where key = $1;

