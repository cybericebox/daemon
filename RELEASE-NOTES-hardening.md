# feat/hardening: notes for the joint release

## Before the release (TODO)

- **Laboratory pin.** The microVM work reads `CapacityResponse.node_profiles` by field name (`labagent.ProfilesOf`, protoreflect), so the daemon builds on the current pin (v1.7.4). At the joint release laboratory is released first; then bump the pin in `go.mod` and move `ProfilesOf` to the typed getters (`GetNodeProfiles`, `NodeProfileCapacity`). Until then an agent without the field reads as an older agent (container only).
- **Migrations.** 0205/0206 (wave 1), 0207 (device type `container` becomes `node` + profile), 0208 (stored node profile report of each agent). 0207 and 0208 were not run against PostgreSQL locally (no Docker); run the integration tests `TestMigration0207TurnsContainersIntoNodes` and `TestAgentNodeProfileReportIsKeptAndOverwritten` in CI.
- **API.** Devices are returned as `Type: "node"` with `Profile`; the frozen frontends do not know it yet (UI pass).
- **PROXY protocol.** `HTTP_SERVER_PROXY_PROTOCOL_FROM` is off by default; the daemon must run with the list set before the edge starts sending the header.
