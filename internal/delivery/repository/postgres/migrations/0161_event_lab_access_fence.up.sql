ALTER TABLE event_lab_access_syncs
 ADD COLUMN operation_id uuid,
 ADD COLUMN policy_fingerprint text NOT NULL DEFAULT '',
 ADD COLUMN expected_group_uid text NOT NULL DEFAULT '',
 ADD COLUMN access_fence_vpn_boot_id text NOT NULL DEFAULT '';
-- Historical applied_revision certified acceptance only; require new physical proof.
UPDATE event_lab_access_syncs SET applied_revision=0;
