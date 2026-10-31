# feat/hardening: notes for the joint release

## Before the release (TODO)

- **Laboratory pin.** The microVM work reads `CapacityResponse.node_profiles` by field name (`labagent.ProfilesOf`, protoreflect), so the daemon builds on the current pin (v1.7.4). At the joint release laboratory is released first; then bump the pin in `go.mod` and move `ProfilesOf` to the typed getters (`GetNodeProfiles`, `NodeProfileCapacity`). Until then an agent without the field reads as an older agent (container only).
- **Migrations.** 0205/0206 (wave 1), 0207 (device type `container` becomes `node` + profile), 0208 (stored node profile report of each agent). 0207 and 0208 have integration tests (`TestMigration0207TurnsContainersIntoNodes`, `TestAgentNodeProfileReportIsKeptAndOverwritten`) that passed locally.
- **API.** Devices are returned as `Type: "node"` with `Profile`; the frozen frontends do not know it yet (UI pass).
- **PROXY protocol.** `HTTP_SERVER_PROXY_PROTOCOL_FROM` is off by default; the daemon must run with the list set before the edge starts sending the header.
- **Labs after 0207.** The lab definition hash is taken from the topology as stored (`node` + profile); labs created before migration 0207 are recreated once in a test database (no production database exists yet).
- **Reveal cohort.** The «show to all» cohort is recomputed until the event starts (an unopened cohort); from the start it is frozen. The SQL (`FreezeEventLabRevealBarrier`, `ListStandTeams`) was edited by hand in the sqlc output and has Docker integration tests (`reveal_cohort_recompute_integration_test.go`) (they passed locally); sqlc output was edited by hand, so run the sqlc generate check in CI.
- **Reserved device names.** `exerciseModel.ReservedDeviceNames` copies `laboratory/pkg/names.ReservedDeviceNames` (a test compares them when the laboratory source is next to the daemon). After the pin bump, import the laboratory package and delete the copy.
- **Unavailable reason codes.** `infraModel.UnavailableCodes` and `ParseUnavailableReason` copy `laboratory/pkg/names` (`UnavailableReasonCodes`, `ParseUnavailableReason`); a test compares the lists when the laboratory source is next to the daemon. After the pin bump import the package and delete the copy.
