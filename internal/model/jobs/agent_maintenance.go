package jobsModel

// AgentMaintenanceArgs drives the upkeep of the infrastructure agents: renewing client certificates
// that end soon and removing rotated-out access keys after their retention.
type AgentMaintenanceArgs struct{}

func (AgentMaintenanceArgs) Kind() string { return "agent_maintenance" }
