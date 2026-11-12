-- The device keys "external" and "terminal" become one list "access" (docs/specs/2026-11-12-device-access-and-checks.md):
--
--   device  external {port, protocol}            -> access entry {kind: http, port, protocol}
--   device  terminal {enabled: true, command, user} -> access entry {kind: terminal, command, user}
--   device  terminal {enabled: false}            -> nothing (it said "off", an absent entry says the same)
--
-- Only exercise_versions.variants stores devices. A device that already has an "access" list and still an old key is
-- ambiguous, so the migration stops with the version id instead of choosing. A device without the old keys is left as it
-- is, so the migration can run again without changing anything.
DO
$$
DECLARE
    version_row  record;
    variant_row  record;
    device_row   record;
    new_variants jsonb;
    new_devices  jsonb;
    entries      jsonb;
    device       jsonb;
BEGIN
    FOR version_row IN
        SELECT id, variants
        FROM exercise_versions
        WHERE jsonb_typeof(variants) = 'array'
          AND jsonb_path_exists(variants, '$[*].topology.devices[*] ? (exists(@.external) || exists(@.terminal))')
        ORDER BY id
        LOOP
            new_variants := '[]'::jsonb;
            FOR variant_row IN
                SELECT v.variant, v.ord
                FROM jsonb_array_elements(version_row.variants) WITH ORDINALITY AS v (variant, ord)
                ORDER BY v.ord
                LOOP
                    new_devices := NULL;
                    IF jsonb_typeof(variant_row.variant #> '{topology,devices}') = 'array' THEN
                        new_devices := '[]'::jsonb;
                        FOR device_row IN
                            SELECT d.device, d.ord
                            FROM jsonb_array_elements(variant_row.variant #> '{topology,devices}')
                                     WITH ORDINALITY AS d (device, ord)
                            ORDER BY d.ord
                            LOOP
                                device := device_row.device;
                                IF NOT (device ? 'external' OR device ? 'terminal') THEN
                                    new_devices := new_devices || jsonb_build_array(device);
                                    CONTINUE;
                                END IF;
                                IF device ? 'access' THEN
                                    RAISE EXCEPTION 'exercise version %: device % has an access list and the former external or terminal key; keep one of them',
                                        version_row.id, COALESCE(device ->> 'name', device_row.ord::text);
                                END IF;
                                entries := '[]'::jsonb;
                                IF jsonb_typeof(device -> 'external') = 'object' THEN
                                    entries := entries || jsonb_build_array(
                                            jsonb_build_object('kind', 'http') || (device -> 'external'));
                                END IF;
                                IF jsonb_typeof(device -> 'terminal') = 'object'
                                    AND COALESCE((device -> 'terminal' ->> 'enabled')::boolean, false) THEN
                                    entries := entries || jsonb_build_array(
                                            jsonb_build_object('kind', 'terminal') || ((device -> 'terminal') - 'enabled'));
                                END IF;
                                device := device - 'external' - 'terminal';
                                IF jsonb_array_length(entries) > 0 THEN
                                    device := device || jsonb_build_object('access', entries);
                                END IF;
                                new_devices := new_devices || jsonb_build_array(device);
                            END LOOP;
                    END IF;
                    new_variants := new_variants || jsonb_build_array(
                            CASE
                                WHEN new_devices IS NULL THEN variant_row.variant
                                ELSE jsonb_set(variant_row.variant, '{topology,devices}', new_devices)
                                END);
                END LOOP;
            UPDATE exercise_versions SET variants = new_variants WHERE id = version_row.id;
        END LOOP;
END
$$;
