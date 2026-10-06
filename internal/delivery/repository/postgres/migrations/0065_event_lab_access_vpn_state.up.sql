ALTER TABLE event_lab_access_syncs
    ADD COLUMN vpn_enabled boolean NOT NULL DEFAULT false;

-- Existing groups may carry the previous runtime-wide suspension state.
-- Revisit every group once so the VPN probe mode and Lab suspension split
-- are applied even when the event's VPN setting is false.
UPDATE event_lab_access_syncs
SET desired_revision = desired_revision + 1;
