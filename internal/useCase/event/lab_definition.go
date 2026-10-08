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
	raw, _ := json.Marshal(topology)
	sum := sha256.Sum256(append([]byte(versionID.String()+"\x00"+strconv.FormatInt(int64(generation), 10)+"\x00"), raw...))
	return hex.EncodeToString(sum[:])
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
