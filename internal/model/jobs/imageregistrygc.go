package jobsModel

// ImageRegistryGCArgs is the (empty) payload of the periodic platform registry tag collection.
type ImageRegistryGCArgs struct{}

func (ImageRegistryGCArgs) Kind() string { return "image_registry_gc" }
