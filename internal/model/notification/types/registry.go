package notificationTypes

var (
	registry          = map[NotificationType]NotificationPayload{}
	descriptorsByType = map[NotificationType][]VariableDescriptor{}
	// internalTypes are registered for channel and variable gating but have no
	// stored templates, so the template catalogs skip them.
	internalTypes = map[NotificationType]bool{}
)

// Register records a prototype and pre-computes its descriptors at once. Called
// only from types/*.go init() (single-goroutine, before main); the maps are
// read-only afterward, so no locking is needed.
func Register(n NotificationPayload) {
	registry[n.NotificationType()] = n
	descriptorsByType[n.NotificationType()] = reflectDescriptors(n)
}

// RegisterInternal records a prototype like Register, but hides the type from
// Types(): custom messages (broadcasts) carry their own content and have no
// template rows.
func RegisterInternal(n NotificationPayload) {
	Register(n)
	internalTypes[n.NotificationType()] = true
}

// TypeInfo is a registered notification type and the channels it supports.
type TypeInfo struct {
	Type     NotificationType
	Channels []NotificationChannel
}

// Types returns every registered notification type with its supported channels.
// Order is unspecified (map iteration); callers that need stable output sort it.
func Types() []TypeInfo {
	out := make([]TypeInfo, 0, len(registry))
	for t, proto := range registry {
		if internalTypes[t] {
			continue
		}
		out = append(out, TypeInfo{Type: t, Channels: proto.NotificationChannels()})
	}
	return out
}

// Supports are barrier L1: reports whether type t may fire on channel c.
func Supports(t NotificationType, c NotificationChannel) bool {
	proto, ok := registry[t]
	if !ok {
		return false
	}
	for _, ch := range proto.NotificationChannels() {
		if ch == c {
			return true
		}
	}
	return false
}
