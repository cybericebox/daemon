-- The shape of a task document becomes cib-exercise/1.0 (docs/specs/2026-11-11-exercise-schema-1.0.md). Only
-- exercise_versions.variants stores it; event and team copies pin the participant-visible part of a task, never its
-- flag or its topology.
--
--   task    flag [..]                          -> check.answer {type: flag, values: [..]} (values only when it had some)
--   task    (new)                              -> key "task_<position>" (stable, 1-based, same in every variant)
--   task    linked_device_id + device_flag_var -> an entry {name: <var>, value: "{{task.<key>}}", secret: false} in
--                                                bootstrap.env of that device
--   device  env_vars                           -> bootstrap.env (name, value, secret and the origin of a secret as they
--                                                are; a sealed value stays sealed under the same name)
--
-- Nothing is dropped silently: a version that cannot be converted stops the migration with its id and the reason
-- (a link to a device the variant does not have, a link without a variable name or a variable without a device,
-- a variable that another variable of the device already uses). A task that already has a "check" is left as it is,
-- so the migration can run again without changing anything. Exercise writes pass the catalog_metadata_writer fence of
-- 0195 through the connection parameter of the migration.
DO
$$
DECLARE
    version_row  record;
    variant_row  record;
    task_row     record;
    device_row   record;
    new_variants jsonb;
    new_tasks    jsonb;
    new_devices  jsonb;
    links        jsonb; -- [{device, var, key}] of one variant
    link         jsonb;
    task_key     text;
    link_var     text;
    old_env      jsonb;
    new_env      jsonb;
    bootstrap    jsonb;
    answer       jsonb;
    seen_names   text[];
BEGIN
    FOR version_row IN
        SELECT id, variants
        FROM exercise_versions
        WHERE jsonb_typeof(variants) = 'array'
          AND (jsonb_path_exists(variants, '$[*].tasks[*] ? (!exists(@.check))')
            OR jsonb_path_exists(variants, '$[*].topology.devices[*].env_vars'))
        ORDER BY id
        LOOP
            new_variants := '[]'::jsonb;
            FOR variant_row IN
                SELECT v.variant, v.ord
                FROM jsonb_array_elements(version_row.variants) WITH ORDINALITY AS v (variant, ord)
                ORDER BY v.ord
                LOOP
                    links := '[]'::jsonb;
                    new_tasks := '[]'::jsonb;
                    IF jsonb_typeof(variant_row.variant -> 'tasks') = 'array' THEN
                        FOR task_row IN
                            SELECT t.task, t.ord
                            FROM jsonb_array_elements(variant_row.variant -> 'tasks') WITH ORDINALITY AS t (task, ord)
                            ORDER BY t.ord
                            LOOP
                                IF task_row.task ? 'check' THEN
                                    new_tasks := new_tasks || jsonb_build_array(task_row.task);
                                    CONTINUE;
                                END IF;
                                task_key := COALESCE(NULLIF(task_row.task ->> 'key', ''), 'task_' || task_row.ord);
                                answer := jsonb_build_object('type', 'flag');
                                IF jsonb_typeof(task_row.task -> 'flag') = 'array'
                                    AND jsonb_array_length(task_row.task -> 'flag') > 0 THEN
                                    answer := answer || jsonb_build_object('values', task_row.task -> 'flag');
                                END IF;
                                link_var := NULLIF(task_row.task ->> 'device_flag_var', '');
                                IF (task_row.task ->> 'linked_device_id') IS NOT NULL AND link_var IS NULL THEN
                                    RAISE EXCEPTION 'exercise version %: variant %, task %: a device is linked but device_flag_var is empty',
                                        version_row.id, variant_row.ord, task_row.ord;
                                END IF;
                                IF (task_row.task ->> 'linked_device_id') IS NULL AND link_var IS NOT NULL THEN
                                    RAISE EXCEPTION 'exercise version %: variant %, task %: device_flag_var is set but no device is linked',
                                        version_row.id, variant_row.ord, task_row.ord;
                                END IF;
                                IF link_var IS NOT NULL THEN
                                    links := links || jsonb_build_array(jsonb_build_object(
                                            'device', task_row.task ->> 'linked_device_id', 'var', link_var, 'key', task_key));
                                END IF;
                                new_tasks := new_tasks || jsonb_build_array(
                                        (task_row.task - 'flag' - 'linked_device_id' - 'device_flag_var')
                                            || jsonb_build_object('key', task_key, 'check', jsonb_build_object('answer', answer)));
                            END LOOP;
                    END IF;

                    -- Every link must name a device of this variant.
                    FOR link IN SELECT l FROM jsonb_array_elements(links) AS l
                        LOOP
                            IF NOT jsonb_path_exists(variant_row.variant, '$.topology.devices[*] ? (@.id == $id)',
                                                     jsonb_build_object('id', link ->> 'device')) THEN
                                RAISE EXCEPTION 'exercise version %: variant %: task "%" is linked to device % that the variant does not have',
                                    version_row.id, variant_row.ord, link ->> 'key', link ->> 'device';
                            END IF;
                        END LOOP;

                    new_devices := NULL;
                    IF jsonb_typeof(variant_row.variant #> '{topology,devices}') = 'array' THEN
                        new_devices := '[]'::jsonb;
                        FOR device_row IN
                            SELECT d.device, d.ord
                            FROM jsonb_array_elements(variant_row.variant #> '{topology,devices}') WITH ORDINALITY AS d (device, ord)
                            ORDER BY d.ord
                            LOOP
                                old_env := CASE WHEN jsonb_typeof(device_row.device -> 'env_vars') = 'array'
                                                    THEN device_row.device -> 'env_vars' ELSE '[]'::jsonb END;
                                new_env := COALESCE(device_row.device #> '{bootstrap,env}', '[]'::jsonb) || old_env;
                                seen_names := ARRAY(SELECT e ->> 'name' FROM jsonb_array_elements(new_env) AS e);
                                FOR link IN
                                    SELECT l FROM jsonb_array_elements(links) AS l WHERE l ->> 'device' = device_row.device ->> 'id'
                                    LOOP
                                        IF (link ->> 'var') = ANY (seen_names) THEN
                                            RAISE EXCEPTION 'exercise version %: variant %, device "%": the variable % of task "%" is already used on the device',
                                                version_row.id, variant_row.ord, device_row.device ->> 'name', link ->> 'var', link ->> 'key';
                                        END IF;
                                        seen_names := seen_names || (link ->> 'var');
                                        new_env := new_env || jsonb_build_array(jsonb_build_object(
                                                'name', link ->> 'var', 'value', '{{task.' || (link ->> 'key') || '}}', 'secret', false));
                                    END LOOP;
                                IF jsonb_array_length(new_env) > 0 THEN
                                    bootstrap := COALESCE(device_row.device -> 'bootstrap', '{}'::jsonb)
                                                     || jsonb_build_object('env', new_env);
                                    new_devices := new_devices || jsonb_build_array(
                                            (device_row.device - 'env_vars') || jsonb_build_object('bootstrap', bootstrap));
                                ELSE
                                    new_devices := new_devices || jsonb_build_array(device_row.device - 'env_vars');
                                END IF;
                            END LOOP;
                    END IF;

                    new_variants := new_variants || jsonb_build_array(
                            CASE
                                WHEN new_devices IS NULL
                                    THEN variant_row.variant || jsonb_build_object('tasks', new_tasks)
                                ELSE jsonb_set(variant_row.variant || jsonb_build_object('tasks', new_tasks),
                                               '{topology,devices}', new_devices)
                                END);
                END LOOP;
            UPDATE exercise_versions SET variants = new_variants WHERE id = version_row.id;
        END LOOP;
END
$$;
