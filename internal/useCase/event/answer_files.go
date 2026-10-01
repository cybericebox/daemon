package event

import (
	"bytes"
	"context"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnswerFileRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

const maxAnswerFileNameLength = 200

// AnswerFileView is an uploaded answer file as the client stores it in the
// answer and shows it.
type AnswerFileView struct {
	ID          uuid.UUID
	Name        string
	Size        int64
	ContentType string
}

func toAnswerFileView(f eventFormModel.AnswerFile) AnswerFileView {
	return AnswerFileView{ID: f.FileID, Name: f.Name, Size: f.SizeBytes, ContentType: f.ContentType}
}

// answerFileName keeps the base name of the client file name, readable and
// bounded; the stored name is only ever sent back in a quoted header or JSON.
func answerFileName(name string) string {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" || !utf8.ValidString(name) {
		return "file"
	}
	if runes := []rune(name); len(runes) > maxAnswerFileNameLength {
		name = string(runes[len(runes)-maxAnswerFileNameLength:])
	}
	return name
}

// answerForm returns the form a file question of the scope belongs to; found
// is false when the event has no such form.
func (u *EventUseCase) answerForm(ctx context.Context, eventID uuid.UUID, scope eventFormModel.AnswerScope) (eventFormModel.Form, bool, error) {
	var (
		form eventFormModel.Form
		err  error
	)
	switch scope {
	case eventFormModel.AnswerScopeParticipant:
		var version eventFormRepo.Version
		version, err = u.forms.Latest(ctx, eventID)
		form = version.Form
	case eventFormModel.AnswerScopeTeam:
		form, err = u.teams.GetFieldConfig(ctx, eventID)
	default:
		return eventFormModel.Form{}, false, nil
	}
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return eventFormModel.Form{}, false, nil
		}
		return eventFormModel.Form{}, false, model.ErrPlatform.WithError(err).WithMessage("Failed to get answer form").Err()
	}
	return form, true, nil
}

// UploadAnswerFile stores a file for a «Файл» question of the event's
// participant form or team fields. The file must match the question's formats
// by content and fit its size limit. It stays with the uploader until an
// answer that references it is saved; unused uploads expire under retention.
func (u *EventUseCase) UploadAnswerFile(ctx context.Context, eventID, actorID uuid.UUID, scope eventFormModel.AnswerScope, fieldKey, name string, r io.Reader) (AnswerFileView, error) {
	form, found, err := u.answerForm(ctx, eventID, scope)
	if err != nil {
		return AnswerFileView{}, err
	}
	field, ok := form.FileFields()[fieldKey]
	if !found || !ok || !form.Enabled {
		return AnswerFileView{}, eventModel.ErrAnswerFileFieldInvalid.Err()
	}
	limit := eventFormModel.FileLimitBytes(field)
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return AnswerFileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read answer file").Err()
	}
	if int64(len(data)) > limit {
		return AnswerFileView{}, eventModel.ErrAnswerFileTooLarge.Err()
	}
	name = answerFileName(name)
	kind, contentType, err := eventFormModel.DetectFileKind(name, data)
	if err != nil || !eventFormModel.AllowsFile(field, kind) {
		return AnswerFileView{}, eventModel.ErrAnswerFileTypeInvalid.Err()
	}
	file, err := u.brandMedia.UploadFile(ctx, name, contentType, bytes.NewReader(data), actorID)
	if err != nil {
		return AnswerFileView{}, err
	}
	answerFile := eventFormModel.NewAnswerFile(file.ID, eventID, scope, fieldKey, name, file.SizeBytes, contentType, actorID, time.Now().UTC())
	if err = u.answerFiles.Create(ctx, answerFile); err != nil {
		return AnswerFileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record answer file").Err()
	}
	return toAnswerFileView(answerFile), nil
}

// boundAnswers are answers whose file references were checked and replaced
// with the stored file metadata, ready to save and then attach.
type boundAnswers struct {
	values   map[string]any
	files    []uuid.UUID
	hasFiles bool
	// missing counts the required team fields the values leave unfilled
	// (only when the organizer asked every team for them).
	missing int32
}

// bindAnswerFiles checks every file answer: the file was uploaded for this
// event, scope and question, by the actor (uuid.Nil is event staff) unless
// the owner's saved answer already holds it. Forms without file questions
// pass through untouched.
func bindAnswerFiles(ctx context.Context, repo *eventAnswerFileRepo.Repository, eventID uuid.UUID, scope eventFormModel.AnswerScope, actor, owner uuid.UUID, form eventFormModel.Form, answers map[string]any) (boundAnswers, error) {
	fields := form.FileFields()
	if len(fields) == 0 {
		return boundAnswers{values: answers}, nil
	}
	ids := make(map[string]uuid.UUID)
	valid := true
	for key := range fields {
		if value, present := answers[key]; present && value != nil && value != "" {
			id, ok := eventFormModel.AnswerFileID(value)
			valid = valid && ok
			ids[key] = id
		}
	}
	bound := boundAnswers{values: make(map[string]any, len(answers)), files: make([]uuid.UUID, 0, len(ids)), hasFiles: true}
	for key, value := range answers {
		bound.values[key] = value
	}
	if len(ids) == 0 {
		return bound, nil
	}
	var rows map[uuid.UUID]eventFormModel.AnswerFile
	if valid {
		list := make([]uuid.UUID, 0, len(ids))
		for _, id := range ids {
			list = append(list, id)
		}
		var err error
		if rows, err = repo.List(ctx, list); err != nil {
			return boundAnswers{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get answer files").Err()
		}
	}
	for key, id := range ids {
		row, found := rows[id]
		if !found || !row.CanAnswer(eventID, scope, key, actor, owner) {
			valid = false
			break
		}
		bound.values[key] = row.Value()
		bound.files = append(bound.files, id)
	}
	if !valid {
		return boundAnswers{}, eventModel.ErrAnswerFileUnavailable.Err()
	}
	return bound, nil
}

// attach gives the saved answer's files to the owner and releases the ones
// the owner's answer no longer references.
func (b boundAnswers) attach(ctx context.Context, repo *eventAnswerFileRepo.Repository, eventID uuid.UUID, scope eventFormModel.AnswerScope, owner uuid.UUID) error {
	if !b.hasFiles {
		return nil
	}
	if err := repo.Attach(ctx, eventID, scope, owner, b.files, time.Now().UTC()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to attach answer files").Err()
	}
	return nil
}

// teamFieldForm reads the team fields for binding file answers; a missing
// configuration has no file questions.
func teamFieldForm(ctx context.Context, repo IRepository, eventID uuid.UUID) (eventFormModel.Form, error) {
	form, err := eventTeamRepo.New(repo).GetFieldConfig(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return eventFormModel.Form{}, nil
		}
		return eventFormModel.Form{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	return form, nil
}

func (u *EventUseCase) streamAnswerFile(ctx context.Context, file eventFormModel.AnswerFile) (io.ReadCloser, AnswerFileView, error) {
	rc, _, err := u.brandMedia.StreamFile(ctx, file.FileID)
	if err != nil {
		return nil, AnswerFileView{}, err
	}
	return rc, toAnswerFileView(file), nil
}

func (u *EventUseCase) eventAnswerFile(ctx context.Context, eventID, fileID uuid.UUID) (eventFormModel.AnswerFile, error) {
	file, err := u.answerFiles.Get(ctx, fileID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return eventFormModel.AnswerFile{}, eventModel.ErrAnswerFileNotFound.Err()
		}
		return eventFormModel.AnswerFile{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get answer file").Err()
	}
	if file.EventID != eventID {
		return eventFormModel.AnswerFile{}, eventModel.ErrAnswerFileNotFound.Err()
	}
	return file, nil
}

// StreamManagedAnswerFile serves any answer file of the event to its staff
// (the route checks read access to the event).
func (u *EventUseCase) StreamManagedAnswerFile(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, AnswerFileView, error) {
	file, err := u.eventAnswerFile(ctx, eventID, fileID)
	if err != nil {
		return nil, AnswerFileView{}, err
	}
	return u.streamAnswerFile(ctx, file)
}

// StreamOwnAnswerFile serves a participant their pending upload, their own
// answer's file or a file of their team's answers.
func (u *EventUseCase) StreamOwnAnswerFile(ctx context.Context, eventID, userID, fileID uuid.UUID) (io.ReadCloser, AnswerFileView, error) {
	file, err := u.eventAnswerFile(ctx, eventID, fileID)
	if err != nil {
		return nil, AnswerFileView{}, err
	}
	var teamID *uuid.UUID
	if participant, getErr := u.participants.Get(ctx, eventID, userID); getErr == nil {
		teamID = participant.TeamID
	} else if !repositoryTools.IsObjectNotFoundError(getErr) {
		return nil, AnswerFileView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event participant").Err()
	}
	if !file.VisibleTo(userID, teamID) {
		return nil, AnswerFileView{}, eventModel.ErrAnswerFileNotFound.Err()
	}
	return u.streamAnswerFile(ctx, file)
}
