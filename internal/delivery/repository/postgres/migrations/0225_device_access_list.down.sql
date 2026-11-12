-- Back to the keys "external" and "terminal". The former shape has one web port and one terminal and nothing for vnc or rdp,
-- so a version that uses vnc or rdp stops the migration instead of losing the entry.
DO
$$
DECLARE
    version_row  record;
    variant_row  record;
    device_row   record;
    entry        jsonb;
    device       jsonb;
    new_variants jsonb;
    new_devices  jsonb;
BEGIN
    FOR version_row IN
        SELECT id, variants
        FROM exercise_versions
        WHERE jsonb_typeof(variants) = 'array'
          AND jsonb_path_exists(variants, '$[*].topology.devices[*] ? (exists(@.access))')
        ORDER BY id
        LOOP
            IF jsonb_path_exists(version_row.variants,
                                 '$[*].topology.devices[*].access[*] ? (@.kind == "vnc" || @.kind == "rdp")') THEN
                RAISE EXCEPTION 'exercise version %: it uses vnc or rdp access, which the former device shape cannot hold; remove it before rolling back',
                    version_row.id;
            END IF;
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
                                IF jsonb_typeof(device -> 'access') = 'array' THEN
                                    FOR entry IN SELECT e FROM jsonb_array_elements(device -> 'access') AS e
                                        LOOP
                                            IF entry ->> 'kind' = 'http' THEN
                                                device := device || jsonb_build_object('external',
                                                        jsonb_build_object('port', entry -> 'port',
                                                                           'protocol', COALESCE(entry ->> 'protocol', 'http')));
                                            ELSIF entry ->> 'kind' = 'terminal' THEN
                                                device := device || jsonb_build_object('terminal',
                                                        (entry - 'kind') || jsonb_build_object('enabled', true));
                                            END IF;
                                        END LOOP;
                                    device := device - 'access';
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
