DROP INDEX event_lab_transitions_client_idx;
DROP INDEX event_vpn_client_minutes_client_idx;
DROP TABLE lab_telemetry_erasures;
DROP FUNCTION lab_blank_client_ip(jsonb, text);
DROP FUNCTION lab_client_has_ip(jsonb, text);
DROP FUNCTION lab_client_name(uuid);
