package resourcesModel

import (
	"sync"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// ProfileOverheads keeps, per agent, the fixed cost each node profile adds to a pod (the microVM RuntimeClass
// overhead the agent reports). A profile costs the most any agent reports, so a plan holds on whichever agent
// takes the group. The container costs nothing. Safe for concurrent use; the zero pointer reads as no overhead.
type ProfileOverheads struct {
	mu      sync.RWMutex
	byAgent map[uuid.UUID]map[exerciseModel.NodeProfile]Amount
}

func NewProfileOverheads() *ProfileOverheads {
	return &ProfileOverheads{byAgent: map[uuid.UUID]map[exerciseModel.NodeProfile]Amount{}}
}

// Set replaces what one agent reports.
func (o *ProfileOverheads) Set(agent uuid.UUID, overheads map[exerciseModel.NodeProfile]Amount) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.byAgent[agent] = overheads
}

// Get is the largest overhead any agent reports for the profile.
func (o *ProfileOverheads) Get(profile exerciseModel.NodeProfile) Amount {
	if o == nil || profile == "" || profile == exerciseModel.NodeProfileContainer {
		return Amount{}
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	var largest Amount
	for _, byProfile := range o.byAgent {
		largest = largest.Max(byProfile[profile])
	}
	return largest
}
