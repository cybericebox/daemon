package jobsModel

// RegistryCheckArgs is the (empty) payload of the periodic check of the image registry credentials.
type RegistryCheckArgs struct{}

func (RegistryCheckArgs) Kind() string { return "registry_credentials_check" }
