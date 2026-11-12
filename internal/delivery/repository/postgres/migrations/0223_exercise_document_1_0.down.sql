-- Back to the former shape of a task: check.answer {type: flag} becomes flag, an env entry of the form
-- {{task.<key>}} becomes the device link of that task (linked_device_id + device_flag_var), bootstrap.env becomes
-- env_vars. The former shape cannot hold a text answer, a state check, a bootstrap file or script, a command, a
-- terminal, a variable or a requirement, so a version that uses any of them stops the migration instead of losing it:
-- roll back only before such content is authored. Task keys are dropped (the former shape has none).
DO
$$
DECLARE
    version_row  record;
    variant_row  record;
    task_row     record;
    device_row   record;
    env_row      record;
    new_variants jsonb;
    new_tasks    jsonb;
    new_devices  jsonb;
    task         jsonb;
    plain_env    jsonb;
    key_ref      text;
    owner_device text;
    owner_var    text;
    links        jsonb;
BEGIN
    FOR version_row IN
        SELECT id, variants FROM exercise_versions WHERE jsonb_typeof(variants) = 'array' ORDER BY id
        LOOP
            IF jsonb_path_exists(version_row.variants, '$[*].tasks[*].check.state')
                OR jsonb_path_exists(version_row.variants, '$[*].tasks[*].check.answer ? (@.type == "text")')
                OR jsonb_path_exists(version_row.variants, '$[*].tasks[*].requires')
                OR jsonb_path_exists(version_row.variants, '$[*].topology.variables')
                OR jsonb_path_exists(version_row.variants,
                                     '$[*].topology.devices[*] ? (exists(@.command) || exists(@.args) || exists(@.system) || exists(@.hardware) || exists(@.terminal) || exists(@.bootstrap.files) || exists(@.bootstrap.script) || exists(@.bootstrap.timeout) || exists(@.bootstrap.interpreter))')
                OR jsonb_path_exists(version_row.variants, '$[*].tasks[*].check.answer.input_hint') THEN
                RAISE EXCEPTION 'exercise version %: it uses what the former document shape cannot hold (a text answer, a state check, requires, variables, a bootstrap file or script, a command or a terminal); remove it before rolling back',
                    version_row.id;
            END IF;
            new_variants := '[]'::jsonb;
            FOR variant_row IN
                SELECT v.variant, v.ord
                FROM jsonb_array_elements(version_row.variants) WITH ORDINALITY AS v (variant, ord)
                ORDER BY v.ord
                LOOP
                    -- device id -> [{var, key}] of the env entries that are task substitutions
                    links := '[]'::jsonb;
                    new_devices := NULL;
                    IF jsonb_typeof(variant_row.variant #> '{topology,devices}') = 'array' THEN
                        new_devices := '[]'::jsonb;
                        FOR device_row IN
                            SELECT d.device, d.ord
                            FROM jsonb_array_elements(variant_row.variant #> '{topology,devices}') WITH ORDINALITY AS d (device, ord)
                            ORDER BY d.ord
                            LOOP
                                plain_env := '[]'::jsonb;
                                FOR env_row IN
                                    SELECT e.entry
                                    FROM jsonb_array_elements(COALESCE(device_row.device #> '{bootstrap,env}', '[]'::jsonb)) WITH ORDINALITY AS e (entry, ord)
                                    ORDER BY e.ord
                                    LOOP
                                        key_ref := substring(env_row.entry ->> 'value' from '^\{\{task\.([a-z][a-z0-9_]{0,31})\}\}$');
                                        IF key_ref IS NOT NULL AND NOT COALESCE((env_row.entry ->> 'secret')::boolean, false) THEN
                                            links := links || jsonb_build_array(jsonb_build_object(
                                                    'device', device_row.device ->> 'id', 'var', env_row.entry ->> 'name', 'key', key_ref));
                                        ELSE
                                            IF (env_row.entry ->> 'value') ~ '\{\{(task|var)\.' THEN
                                                RAISE EXCEPTION 'exercise version %: device "%": the env value of % uses a substitution the former shape cannot hold',
                                                    version_row.id, device_row.device ->> 'name', env_row.entry ->> 'name';
                                            END IF;
                                            plain_env := plain_env || jsonb_build_array(env_row.entry);
                                        END IF;
                                    END LOOP;
                                new_devices := new_devices || jsonb_build_array(
                                        CASE WHEN jsonb_array_length(plain_env) > 0
                                                 THEN (device_row.device - 'bootstrap') || jsonb_build_object('env_vars', plain_env)
                                             ELSE device_row.device - 'bootstrap' END);
                            END LOOP;
                    END IF;
                    new_tasks := '[]'::jsonb;
                    IF jsonb_typeof(variant_row.variant -> 'tasks') = 'array' THEN
                        FOR task_row IN
                            SELECT t.task, t.ord
                            FROM jsonb_array_elements(variant_row.variant -> 'tasks') WITH ORDINALITY AS t (task, ord)
                            ORDER BY t.ord
                            LOOP
                                task := task_row.task - 'key' - 'check';
                                IF task_row.task #> '{check,answer,values}' IS NOT NULL THEN
                                    task := task || jsonb_build_object('flag', task_row.task #> '{check,answer,values}');
                                END IF;
                                owner_device := NULL;
                                owner_var := NULL;
                                SELECT l ->> 'device', l ->> 'var'
                                INTO owner_device, owner_var
                                FROM jsonb_array_elements(links) AS l
                                WHERE l ->> 'key' = task_row.task ->> 'key'
                                LIMIT 1;
                                IF owner_device IS NOT NULL THEN
                                    task := task || jsonb_build_object('linked_device_id', owner_device, 'device_flag_var', owner_var);
                                END IF;
                                new_tasks := new_tasks || jsonb_build_array(task);
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
