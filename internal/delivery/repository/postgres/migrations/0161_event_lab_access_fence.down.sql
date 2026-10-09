ALTER TABLE event_lab_access_syncs
 DROP COLUMN operation_id,
 DROP COLUMN policy_fingerprint,
 DROP COLUMN expected_group_uid,
 DROP COLUMN access_fence_vpn_boot_id;
