package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	mediaUseCase "github.com/cybericebox/daemon/internal/useCase/media"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type replaySources struct {
	*mediaUseCase.MediaUseCase
	opens int
}

func (s *replaySources) OpenUploadAt(ctx context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.Upload, error) {
	s.opens++
	return s.MediaUseCase.OpenUploadAt(ctx, id)
}
func (s *replaySources) OpenFileAt(ctx context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.File, error) {
	s.opens++
	return s.MediaUseCase.OpenFileAt(ctx, id)
}

type replayRegistry struct{ pushes int }

func (*replayRegistry) RequireImageUploads(context.Context) error { return nil }
func (*replayRegistry) IndexImageArchive(open ociregistry.ArchiveSource, size int64) (*ociregistry.ImageArchive, error) {
	return ociregistry.ReadImageArchive(open, size, imageModel.MaxImageArchiveBytes)
}
func (*replayRegistry) InspectImageArchive(_ context.Context, a *ociregistry.ImageArchive, name string) (imageUseCase.ArchiveImageInspection, error) {
	layers := []imageUseCase.Descriptor{}
	for _, l := range a.Layers {
		layers = append(layers, imageUseCase.Descriptor{MediaType: l.MediaType, Digest: l.Digest, Size: l.Size})
	}
	manifest, err := imageModel.BuildManifest(imageUseCase.Descriptor{MediaType: a.Config.MediaType, Digest: a.Config.Digest, Size: a.Config.Size}, layers)
	if err != nil {
		return imageUseCase.ArchiveImageInspection{}, err
	}
	ref, err := imageModel.NewPlatformRef(name, manifest.Digest)
	return imageUseCase.ArchiveImageInspection{Reference: ref.String(), Digest: manifest.Digest, Reused: true}, err
}
func (r *replayRegistry) PushImageArchive(ctx context.Context, in imageUseCase.PushArchiveInput) (imageUseCase.CreateImageResult, error) {
	r.pushes++
	inspection, err := r.InspectImageArchive(ctx, in.Archive, in.Name)
	return imageUseCase.CreateImageResult{Reference: inspection.Reference}, err
}

// Both real source HTTP handlers delegate to the actual use case, PostgreSQL
// jobs/catalog/media, and real archive readers. Only immutable object bytes and
// registry transport/queue dispatch are local test doubles.
func TestSourceHTTPRecoversOwnedActiveAndCompletedReimport(t *testing.T) {
	for _, route := range []string{"upload", "uploaded"} {
		for _, state := range []string{"active", "done"} {
			t.Run(route+"-"+state, func(t *testing.T) {
				db := testhelpers.SetupTestDB(t)
				ctx := context.Background()
				now := time.Now()
				user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "replay@test.test", now)
				user.Role = rbac.RoleSuperAdmin
				_, err := userRepo.New(db.Queries).Create(ctx, user)
				require.NoError(t, err)
				repo := exerciseRepo.New(db.Queries)
				entity, err := exerciseModel.NewExercise("Replay original", "", nil, user.ID, now)
				require.NoError(t, err)
				entity, err = repo.Create(ctx, entity)
				require.NoError(t, err)
				draft, err := repo.UpsertDraft(ctx, entity.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "before"}, now, uuid.NullUUID{})
				require.NoError(t, err)
				archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Replay updated"},"versions":[{"status":"draft","variants":[{"topology":{"devices":[{"name":"web","type":"container","image":{"file":"images/web.tar"}}]}}]}]}`), "images/web.tar": archivetest.Legacy(archivetest.Amd64([]byte("replay layer")))})
				require.NoError(t, err)
				media := mediaUseCase.NewMediaUseCase(mediaUseCase.Dependencies{Repo: db.Queries, Storage: testhelpers.NewMemoryStorage(), Config: config.MediaConfig{MaxUploadBytes: 64 << 20, UploadChunkBytes: 64 << 20, UploadTTL: time.Hour}})
				sources := &replaySources{MediaUseCase: media}
				registry := &replayRegistry{}
				in := exercise.PreviewArchiveInput{Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: entity.ID}}}
				if route == "uploaded" {
					f, err := media.UploadFile(ctx, "source.zip", "application/zip", bytes.NewReader(archive), user.ID)
					require.NoError(t, err)
					in.FileID = f.ID
				} else {
					up, err := media.StartUpload(ctx, "source.zip", "application/zip", int64(len(archive)), user.ID, mediaModel.UploadPurposeExerciseArchive)
					require.NoError(t, err)
					_, err = media.PutChunk(ctx, up.ID, user.ID, 0, bytes.NewReader(archive))
					require.NoError(t, err)
					in.UploadID = up.ID
				}
				var args jobsModel.ExerciseImportArgs
				queued := 0
				uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: media, CatalogUoW: postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))})
				uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources, Images: registry, Queue: func(_ context.Context, a jobsModel.ExerciseImportArgs) error { args = a; queued++; return nil }})
				actor := exercise.Actor{UserID: user.ID, Role: user.Role}
				preview, err := uc.PreviewArchiveImport(ctx, actor, in)
				require.NoError(t, err)
				request := exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}}
				fake := &fakeUC{archiveImport: archiveImportCalls{commit: uc.CommitArchiveImport}}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				endpoint := "/api/exercises/import/" + route
				response := jsonRequest(newEngine(fake, user.ID), http.MethodPost, endpoint, string(body))
				require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
				jobID := args.JobID
				require.NotEqual(t, uuid.Nil, jobID)
				if state == "done" {
					require.NoError(t, uc.RunArchiveImport(ctx, args))
					job, err := uc.ArchiveImportJob(ctx, jobID, user.ID)
					require.NoError(t, err)
					require.Equal(t, exerciseModel.ImportJobDone, job.State)
					if route == "upload" {
						_, err := media.UploadStatus(ctx, in.UploadID, user.ID)
						require.Error(t, err, "successful source was consumed")
					}
				} else {
					_, err = repo.UpsertDraft(ctx, entity.ID, draft.ID, exerciseModel.ExerciseVersion{AdminNote: "changed after queue"}, time.Now(), uuid.NullUUID{})
					require.NoError(t, err)
				}
				before, err := repo.GetDraft(ctx, entity.ID)
				require.NoError(t, err)
				opens, pushes := sources.opens, registry.pushes
				response = jsonRequest(newEngine(fake, user.ID), http.MethodPost, endpoint, string(body))
				require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), jobID.String())
				require.Equal(t, opens, sources.opens, "durable replay does not reopen archive bytes")
				require.Equal(t, pushes, registry.pushes)
				require.Equal(t, 1, queued)
				after, err := repo.GetDraft(ctx, entity.ID)
				require.NoError(t, err)
				require.Equal(t, before, after)
				changed := request
				changed.ConfirmedUpdates = append([]exercise.ConfirmedImportUpdate(nil), request.ConfirmedUpdates...)
				changed.ConfirmedUpdates[0].Revision += "other-plan"
				changedBody, err := json.Marshal(changed)
				require.NoError(t, err)
				response = jsonRequest(newEngine(fake, user.ID), http.MethodPost, endpoint, string(changedBody))
				require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
				require.Equal(t, opens, sources.opens)
				foreign := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "foreign-replay@test.test", now)
				foreign.Role = rbac.RoleSuperAdmin
				_, err = userRepo.New(db.Queries).Create(ctx, foreign)
				require.NoError(t, err)
				response = jsonRequest(newEngine(fake, foreign.ID), http.MethodPost, endpoint, string(body))
				require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), jobID.String())
				if state == "active" {
					job, err := repo.GetImportJob(ctx, jobID)
					require.NoError(t, err)
					job.Fail(exerciseModel.ImportJobError{}, time.Now())
					_, err = repo.SaveImportJob(ctx, job)
					require.NoError(t, err)
					// A restart is an execution, so the now-stale confirmation is checked.
					response = jsonRequest(newEngine(fake, user.ID), http.MethodPost, endpoint, string(body))
					require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "72906")
					table, id := "media_uploads", in.UploadID
					if route == "uploaded" {
						table, id = "files", in.FileID
					}
					_, err = db.Pool.Exec(ctx, `DELETE FROM `+table+` WHERE id=$1`, id)
					require.NoError(t, err)
					response = jsonRequest(newEngine(fake, user.ID), http.MethodPost, endpoint, string(body))
					require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
					job, err = repo.GetImportJob(ctx, jobID)
					require.NoError(t, err)
					require.Equal(t, exerciseModel.ImportJobFailed, job.State)
					require.EqualValues(t, 1, job.Generation)
					require.Equal(t, 1, queued, "invalid restarts must not touch the durable job or queue")

				}
			})
		}
	}
}
