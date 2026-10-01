package event

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// maxBatchPeople bounds one team batch like one invitation batch.
const maxBatchPeople = 200

// Batch issue codes: the client maps each to its own message and CSV row.
const (
	BatchIssueTeamName      = "team_name"
	BatchIssueTeamExists    = "team_exists"
	BatchIssueTeamTooSmall  = "team_too_small"
	BatchIssueTeamTooLarge  = "team_too_large"
	BatchIssueTooManyTeams  = "too_many_teams"
	BatchIssueTooManyPeople = "too_many_people"
	BatchIssueCaptain       = "captain_invalid"
	BatchIssueEmailInvalid  = "email_invalid"
	BatchIssueEmailRepeated = "email_repeated"
	BatchIssueInTeam        = "already_in_team"
	BatchIssueParticipant   = "already_participant"
	BatchIssueAccount       = "account_unavailable"
	BatchIssueStaff         = "staff_cannot_participate"
	BatchIssueTeamFields    = "team_fields_invalid"
	BatchIssueMemberFields  = "member_fields_invalid"
)

// BatchTeamMemberInput is one roster line; the names prefill the pending
// account an invitation creates.
type BatchTeamMemberInput struct {
	Email     string
	FirstName string
	LastName  string
	// Fields prefill the person's participant form answers; only used for
	// people who are not approved participants yet.
	Fields map[string]any
}

// BatchTeamInput is one team of a batch. The captain is one of the members.
type BatchTeamInput struct {
	Name         string
	CaptainEmail string
	Members      []BatchTeamMemberInput
	Fields       map[string]any
	// Partial is a CSV import: team and member fields are checked by type
	// only, required ones may stay empty (people complete them later).
	Partial bool
}

// BatchTeamIssue points at a team of the request (Team = -1 for the whole
// batch) and, when it is about one person, at the address.
type BatchTeamIssue struct {
	Team  int
	Email string
	Code  string
}

type BatchTeamOutcome struct {
	ID      uuid.UUID
	Name    string
	Created bool
}

// BatchTeamsResult: with any issue nothing was written. Otherwise the teams
// exist, approved participants are members, everyone else holds a pending
// team invitation. NotSent lists invitations whose email failed to queue;
// they stay pending and can be resent.
type BatchTeamsResult struct {
	Issues    []BatchTeamIssue
	Teams     []BatchTeamOutcome
	Assigned  int
	Invited   int
	Unchanged int
	NotSent   []string
}

type batchMember struct {
	BatchTeamMemberInput
	captain bool
}

type batchTeam struct {
	bound   boundAnswers
	name    string
	fields  map[string]any
	partial bool
	members []batchMember
}

type pendingInvitation struct {
	user     userModel.User
	teamName string
}

// CreateManagedTeams builds teams from moderator input (the manual dialog or a
// CSV) in one transaction: every team and every address is validated first,
// and a single issue writes nothing. It is idempotent per address: a repeat
// finds the team by name (same captain) and skips people already on it.
// dryRun runs every check and count, then rolls back and sends nothing: the
// preview of a CSV import.
func (u *EventUseCase) CreateManagedTeams(ctx context.Context, eventID, by uuid.UUID, in []BatchTeamInput, dryRun bool) (BatchTeamsResult, error) {
	if u.uow == nil || u.invitationNotifier == nil || u.setupTokens == nil || u.eventDomain == "" {
		return BatchTeamsResult{}, model.ErrPlatform.WithMessage("Event invitation dependencies are not configured").Err()
	}
	teams, issues := normalizeBatchTeams(in)
	if len(issues) > 0 {
		return BatchTeamsResult{Issues: issues}, nil
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return BatchTeamsResult{}, err
	}
	defer unit.Restore()
	if _, err = txRepo.LockEventForTeamChange(txCtx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return BatchTeamsResult{}, eventModel.ErrEventNotFound.Err()
		}
		return BatchTeamsResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to lock event for team creation").Err()
	}
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return BatchTeamsResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	if config.Participation == nil || *config.Participation != eventConfigModel.ParticipationTeam {
		return BatchTeamsResult{}, eventTeamModel.ErrEventTeamParticipationInvalid.Err()
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		return BatchTeamsResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	teamRepo, participants, users := eventTeamRepo.New(txRepo), participantRepo.New(txRepo), userRepo.New(txRepo)

	var memberForm *eventFormRepo.Version
	formLoaded := false
	for _, team := range teams {
		for _, member := range team.members {
			if len(member.Fields) > 0 && !formLoaded {
				formLoaded = true
				if memberForm, err = prefillForm(txCtx, eventFormRepo.New(txRepo), eventID); err != nil {
					return BatchTeamsResult{}, err
				}
			}
		}
	}
	for i, team := range teams {
		for _, member := range team.members {
			if !validatePrefill(memberForm, member.Fields) {
				issues = append(issues, BatchTeamIssue{Team: i, Email: member.Email, Code: BatchIssueMemberFields})
			}
		}
	}

	existing := make([]*eventTeamModel.EventTeam, len(teams))
	newTeams := 0
	for i, team := range teams {
		found, lookupErr := teamRepo.GetByName(txCtx, eventID, team.name)
		switch {
		case lookupErr == nil:
			existing[i] = &found
		case repositoryTools.IsObjectNotFoundError(lookupErr):
			newTeams++
		default:
			return BatchTeamsResult{}, model.ErrPlatform.WithError(lookupErr).WithMessage("Failed to get event team").Err()
		}
		if existing[i] == nil {
			size := int32(len(team.members))
			if size > config.MaxTeamSize {
				issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamTooLarge})
			} else if size < config.EffectiveMinTeamSize() {
				issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamTooSmall})
			}
		}
		if len(team.fields) > 0 {
			bound, fieldErr := validateTeamFieldAnswers(txCtx, txRepo, eventID, uuid.Nil, uuid.Nil, team.fields, team.partial)
			if fieldErr != nil {
				if !errors.Is(fieldErr, eventTeamModel.ErrEventTeamFieldsInvalid.Err()) {
					return BatchTeamsResult{}, fieldErr
				}
				issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamFields})
			} else if len(bound.files) > 0 {
				return BatchTeamsResult{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Files cannot be attached to teams built in a batch").Err()
			} else {
				teams[i].bound = bound
			}
		}
	}
	if config.MaxTeams != nil && newTeams > 0 {
		count, countErr := txRepo.CountEventTeams(txCtx, eventID)
		if countErr != nil {
			return BatchTeamsResult{}, model.ErrPlatform.WithError(countErr).WithMessage("Failed to count event teams").Err()
		}
		if count+int64(newTeams) > int64(*config.MaxTeams) {
			issues = append(issues, BatchTeamIssue{Team: -1, Code: BatchIssueTooManyTeams})
		}
	}

	now := time.Now().UTC()
	result := BatchTeamsResult{}
	var invitations []pendingInvitation
	for i, team := range teams {
		// Resolve every address before any team write, so the captain's
		// account id exists and every issue of the team is reported at once.
		type resolved struct {
			member      batchMember
			user        userModel.User
			participant *participantModel.Participant
		}
		people := make([]resolved, 0, len(team.members))
		var captainID uuid.UUID
		for _, member := range team.members {
			user, userErr := users.GetByEmail(txCtx, member.Email)
			if repositoryTools.IsObjectNotFoundError(userErr) {
				pendingUser := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), member.Email, now)
				pendingUser.FirstName, pendingUser.LastName = member.FirstName, member.LastName
				user, userErr = users.Create(txCtx, pendingUser)
			}
			if userErr != nil {
				return BatchTeamsResult{}, model.ErrPlatform.WithError(userErr).WithMessage("Failed to prepare invited account").Err()
			}
			if user.Status != userModel.UserStatusActive && user.Status != userModel.UserStatusIncomplete {
				issues = append(issues, BatchTeamIssue{Team: i, Email: member.Email, Code: BatchIssueAccount})
				continue
			}
			if staff, staffErr := u.isEventStaff(txCtx, eventID, user.ID); staffErr != nil {
				return BatchTeamsResult{}, staffErr
			} else if staff {
				issues = append(issues, BatchTeamIssue{Team: i, Email: member.Email, Code: BatchIssueStaff})
				continue
			}
			var current *participantModel.Participant
			if p, getErr := participants.Get(txCtx, eventID, user.ID); getErr == nil {
				current = &p
			} else if !repositoryTools.IsObjectNotFoundError(getErr) {
				return BatchTeamsResult{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event participant").Err()
			}
			if code := batchMemberConflict(current, existing[i]); code != "" {
				issues = append(issues, BatchTeamIssue{Team: i, Email: member.Email, Code: code})
				continue
			}
			if member.captain {
				captainID = user.ID
			}
			people = append(people, resolved{member: member, user: user, participant: current})
		}
		if existing[i] != nil && captainID != uuid.Nil && existing[i].CaptainID != captainID {
			issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamExists})
		}
		if len(issues) > 0 {
			continue
		}

		var target eventTeamModel.EventTeam
		if existing[i] != nil {
			target = *existing[i]
			pending, countErr := participants.CountPendingTeamInvitations(txCtx, eventID, target.ID)
			if countErr != nil {
				return BatchTeamsResult{}, model.ErrPlatform.WithError(countErr).WithMessage("Failed to count pending team invitations").Err()
			}
			added := 0
			for _, person := range people {
				if !batchMemberOnTeam(person.participant, target.ID) {
					added++
				}
			}
			if int64(target.MemberCount)+pending+int64(added) > int64(config.MaxTeamSize) {
				issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamTooLarge})
				continue
			}
			result.Teams = append(result.Teams, BatchTeamOutcome{ID: target.ID, Name: target.Name})
		} else {
			draft, newErr := eventTeamModel.NewManaged(eventID, captainID, team.name, uuid.Must(uuid.NewV4()).String(), now)
			if newErr != nil {
				return BatchTeamsResult{}, newErr
			}
			created, createErr := teamRepo.Create(txCtx, draft)
			if createErr != nil {
				if creator, ok := repositoryTools.UniqueViolationError(createErr, eventTeamModel.ErrEventTeamExists); ok {
					return BatchTeamsResult{}, creator.Err()
				}
				return BatchTeamsResult{}, model.ErrPlatform.WithError(createErr).WithMessage("Failed to create event team").Err()
			}
			if len(team.fields) > 0 {
				if fieldErr := saveTeamFields(txCtx, txRepo, eventID, created.ID, team.bound); fieldErr != nil {
					return BatchTeamsResult{}, model.ErrPlatform.WithError(fieldErr).WithMessage("Failed to save team fields").Err()
				}
			}
			target = created
			result.Teams = append(result.Teams, BatchTeamOutcome{ID: target.ID, Name: target.Name, Created: true})
		}

		assigned := false
		for _, person := range people {
			p := person.participant
			switch {
			case batchMemberOnTeam(p, target.ID):
				result.Unchanged++
			case p == nil:
				if _, _, inviteErr := participants.Invite(txCtx, eventID, person.user.ID, by, uuid.NullUUID{UUID: target.ID, Valid: true}, now); inviteErr != nil {
					return BatchTeamsResult{}, model.ErrPlatform.WithError(inviteErr).WithMessage("Failed to create participant invitation").Err()
				}
				invitations = append(invitations, pendingInvitation{user: person.user, teamName: target.Name})
				if err = savePersonFields(txCtx, txRepo, eventID, person.user.ID, memberForm, person.member.Fields, now); err != nil {
					return BatchTeamsResult{}, err
				}
			case p.Status == participantModel.StatusPending:
				// A plain event invitation still pending: it becomes a team one.
				if affected, moveErr := participants.SetInvitedTeam(txCtx, eventID, person.user.ID, target.ID); moveErr != nil {
					return BatchTeamsResult{}, model.ErrPlatform.WithError(moveErr).WithMessage("Failed to move invitation to the team").Err()
				} else if affected == 0 {
					return BatchTeamsResult{}, participantModel.ErrInvitationRequired.Err()
				}
				invitations = append(invitations, pendingInvitation{user: person.user, teamName: target.Name})
				if err = savePersonFields(txCtx, txRepo, eventID, person.user.ID, memberForm, person.member.Fields, now); err != nil {
					return BatchTeamsResult{}, err
				}
			default:
				role := participantModel.TeamRoleMember
				if person.member.captain {
					role = participantModel.TeamRoleCaptain
				}
				if err = p.AssignTeam(target.ID, role); err != nil {
					return BatchTeamsResult{}, err
				}
				if affected, addErr := teamRepo.TryAddMember(txCtx, eventID, target.ID, config.MaxTeamSize, now); addErr != nil {
					return BatchTeamsResult{}, model.ErrPlatform.WithError(addErr).WithMessage("Failed to reserve team membership").Err()
				} else if affected == 0 {
					return BatchTeamsResult{}, eventTeamModel.ErrEventTeamFull.Err()
				}
				if affected, assignErr := participants.AssignTeam(txCtx, *p); assignErr != nil {
					return BatchTeamsResult{}, model.ErrPlatform.WithError(assignErr).WithMessage("Failed to assign event team").Err()
				} else if affected == 0 {
					return BatchTeamsResult{}, participantModel.ErrParticipantTeamInvalid.Err()
				}
				result.Assigned++
				assigned = true
			}
		}
		if assigned {
			if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, target.ID, now); err != nil {
				return BatchTeamsResult{}, err
			}
		}
	}
	if len(issues) > 0 {
		return BatchTeamsResult{Issues: issues}, nil
	}
	if dryRun {
		result.Invited = len(invitations)
		return result, nil
	}
	if err = unit.Save(); err != nil {
		return BatchTeamsResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event teams").Err()
	}
	for _, invitation := range invitations {
		if _, sendErr := u.sendParticipantInvitation(ctx, e, invitation.user, true, invitation.teamName, by); sendErr != nil {
			result.NotSent = append(result.NotSent, invitation.user.Email)
		}
	}
	result.Invited = len(invitations)
	return result, nil
}

// normalizeBatchTeams applies the checks that need no database: names, the
// captain mark, addresses, repeats across the whole batch and the batch size.
func normalizeBatchTeams(in []BatchTeamInput) ([]batchTeam, []BatchTeamIssue) {
	var issues []BatchTeamIssue
	teams := make([]batchTeam, 0, len(in))
	seenEmails := map[string]struct{}{}
	seenNames := map[string]struct{}{}
	people := 0
	for i, raw := range in {
		name, nameErr := eventTeamModel.ValidateName(raw.Name)
		if nameErr != nil {
			issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamName})
		} else if _, dup := seenNames[strings.ToLower(name)]; dup {
			issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueTeamExists})
		}
		seenNames[strings.ToLower(name)] = struct{}{}
		captain := normalizeInvitationEmail(raw.CaptainEmail)
		team := batchTeam{name: name, fields: raw.Fields, partial: raw.Partial}
		hasCaptain := false
		for _, member := range raw.Members {
			email := normalizeInvitationEmail(member.Email)
			if !validInvitationEmail(email) {
				issues = append(issues, BatchTeamIssue{Team: i, Email: email, Code: BatchIssueEmailInvalid})
				continue
			}
			if _, dup := seenEmails[email]; dup {
				issues = append(issues, BatchTeamIssue{Team: i, Email: email, Code: BatchIssueEmailRepeated})
				continue
			}
			seenEmails[email] = struct{}{}
			people++
			isCaptain := email == captain
			hasCaptain = hasCaptain || isCaptain
			team.members = append(team.members, batchMember{
				BatchTeamMemberInput: BatchTeamMemberInput{Email: email, FirstName: strings.TrimSpace(member.FirstName), LastName: strings.TrimSpace(member.LastName), Fields: member.Fields},
				captain:              isCaptain,
			})
		}
		if !hasCaptain {
			issues = append(issues, BatchTeamIssue{Team: i, Code: BatchIssueCaptain})
		}
		teams = append(teams, team)
	}
	if len(in) == 0 || people > maxBatchPeople {
		issues = append(issues, BatchTeamIssue{Team: -1, Code: BatchIssueTooManyPeople})
	}
	return teams, issues
}

func normalizeInvitationEmail(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) }

func validInvitationEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && len(email) <= 254
}

// batchMemberConflict says why a person cannot join the batch team, or "" if
// they can: no row yet, an approved participant without a team, a pending
// plain invitation, or already on (or invited to) this very team.
func batchMemberConflict(p *participantModel.Participant, team *eventTeamModel.EventTeam) string {
	if p == nil {
		return ""
	}
	if team != nil && batchMemberOnTeam(p, team.ID) {
		return ""
	}
	switch {
	case p.Status == participantModel.StatusApproved && p.TeamID == nil:
		return ""
	case p.Status == participantModel.StatusApproved:
		return BatchIssueInTeam
	case p.Status == participantModel.StatusPending && p.Invited && !p.InvitedToTeam:
		return ""
	case p.Status == participantModel.StatusPending && p.Invited:
		return BatchIssueInTeam
	default:
		return BatchIssueParticipant
	}
}

func batchMemberOnTeam(p *participantModel.Participant, teamID uuid.UUID) bool {
	if p == nil {
		return false
	}
	if p.TeamID != nil && *p.TeamID == teamID {
		return true
	}
	return p.Status == participantModel.StatusPending && p.InvitedToTeam && p.InvitedTeamID != nil && *p.InvitedTeamID == teamID
}

func savePersonFields(ctx context.Context, repo IRepository, eventID, userID uuid.UUID, form *eventFormRepo.Version, fields map[string]any, now time.Time) error {
	if form == nil || len(fields) == 0 {
		return nil
	}
	return savePrefilledAnswers(ctx, eventFormRepo.New(repo), eventID, userID, *form, fields, now)
}
