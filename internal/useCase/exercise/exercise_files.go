package exercise

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// FileLookup answers whether a task already stores a content; Found false leaves the other fields empty.
type FileLookup struct {
	Found  bool
	FileID uuid.UUID
	Name   string
	Size   int64
}

// LookupExerciseFile tells the editor whether the task already stores the bytes with this SHA-256, so an upload
// can skip them and attach the stored file. Only this task's files are searched: the answer never reveals that
// another task (or anyone else) holds the content. Route gate: exercises write on the task.
func (u *ExerciseUseCase) LookupExerciseFile(ctx context.Context, exerciseID uuid.UUID, sha256 string) (FileLookup, error) {
	stored, err := u.exercises.FileByHash(ctx, exerciseID, strings.ToLower(sha256))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return FileLookup{}, nil
		}
		return FileLookup{}, model.ErrPlatform.WithError(err).WithMessage("Failed to look up an exercise file").Err()
	}
	return FileLookup{Found: true, FileID: stored.ID, Name: stored.Name, Size: stored.Size}, nil
}

// adoptFiles stores every attached content once per task: each distinct SHA-256 is claimed for the task, and an
// attachment whose file is a second copy of content the task already holds is pointed at the task's file (the
// name shown to participants stays). The dropped copy is not deleted here: the media GC removes it once no
// version references it, so a file an older version still links stays.
func (u *ExerciseUseCase) adoptFiles(ctx context.Context, exerciseID uuid.UUID, variants []exerciseModel.Variant) error {
	ids := exerciseModel.CollectFileIDs(variants)
	if len(ids) == 0 {
		return nil
	}
	hashes, err := u.exercises.FileHashes(ctx, ids)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read attached file hashes").Err()
	}
	now := time.Now()
	canonicalByHash := make(map[string]uuid.UUID, len(hashes))
	canonical := make(map[uuid.UUID]uuid.UUID, len(ids))
	// The first attachment of a content (in variant order) is the one offered as the task's copy.
	for _, id := range ids {
		hash, ok := hashes[id]
		if !ok {
			continue
		}
		if _, seen := canonical[id]; seen {
			continue
		}
		stored, claimed := canonicalByHash[hash]
		if !claimed {
			stored, err = u.exercises.ClaimFile(ctx, exerciseID, hash, id, now)
			if err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to store an exercise file").Err()
			}
			canonicalByHash[hash] = stored
		}
		canonical[id] = stored
	}
	for v := range variants {
		for t := range variants[v].Tasks {
			for a := range variants[v].Tasks[t].Attachments {
				attachment := &variants[v].Tasks[t].Attachments[a]
				if stored, ok := canonical[attachment.FileID]; ok {
					attachment.FileID = stored
				}
			}
		}
	}
	return nil
}
