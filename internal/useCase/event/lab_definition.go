package event

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/gofrs/uuid"
	"strconv"
)

func definitionHash(versionID uuid.UUID, generation int32, topology exerciseModel.Topology) string {
	raw, _ := json.Marshal(hashedTopology(topology))
	sum := sha256.Sum256(append([]byte(versionID.String()+"\x00"+strconv.FormatInt(int64(generation), 10)+"\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// hashedTopology is the topology as the stored hashes saw it: a node with the container profile is written as the
// former container device, so the labs made before node profiles keep a valid definition hash; other profiles are new
// and hash as they are.
func hashedTopology(topology exerciseModel.Topology) exerciseModel.Topology {
	devices := make([]exerciseModel.Device, len(topology.Devices))
	copy(devices, topology.Devices)
	for i, d := range devices {
		if d.IsNode() && d.EffectiveProfile() == exerciseModel.NodeProfileContainer {
			devices[i].Type, devices[i].Profile = exerciseModel.DeviceTypeContainer, ""
		}
	}
	topology.Devices = devices
	return topology
}

func (u *EventUseCase) knownZeroDemand(ctx context.Context, q IRepository, l eventLabModel.Lab) (bool, error) {
	if l.DefinitionVersionID == uuid.Nil || l.DefinitionHash == "" {
		return false, nil
	}
	v, err := exerciseRepo.New(q).GetVersion(ctx, l.DefinitionVersionID)
	if err != nil {
		return false, err
	}
	if l.VariantIndex < 0 || int(l.VariantIndex) >= len(v.Variants) {
		return false, nil
	}
	t := v.Variants[l.VariantIndex].Topology
	return len(t.Devices) > 0 && u.resourcePolicy().Total(t).Amount == (resourcesModel.Amount{}) && definitionHash(v.ID, l.Generation, t) == l.DefinitionHash, nil
}
