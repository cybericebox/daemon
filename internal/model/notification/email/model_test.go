package emailModel_test

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
)

func TestNewDraft_DomainDefaults(t *testing.T) {
	by := uuid.Must(uuid.NewV7())
	tpl := emailModel.NewDraft(emailModel.CreateTemplateInput{
		NotificationType: "user_invitation",
		Subject:          "s",
		Preheader:        "p",
		Body:             json.RawMessage(`{"blocks":[]}`),
		Styling:          json.RawMessage(`{}`),
		UpdatedBy:        by,
	})

	if tpl.ID == uuid.Nil {
		t.Fatal("draft id must be generated in the domain")
	}
	if !tpl.IsDraft() || tpl.Status != notificationModel.TemplateStatusDraft {
		t.Fatalf("new template must be a draft: %+v", tpl.Status)
	}
	if tpl.UpdatedByUserID == nil || *tpl.UpdatedByUserID != by {
		t.Fatalf("author not recorded: %+v", tpl.UpdatedByUserID)
	}
	if tpl.NotificationType != "user_invitation" || tpl.Subject != "s" {
		t.Fatalf("fields not applied: %+v", tpl)
	}
}

func TestEmailTemplate_StatusPredicates(t *testing.T) {
	tpl := emailModel.EmailTemplate{Status: notificationModel.TemplateStatusPublished}
	if tpl.IsDraft() || !tpl.IsPublished() || tpl.IsUnpublished() {
		t.Fatalf("predicates wrong for published: %+v", tpl.Status)
	}
	tpl.Status = notificationModel.TemplateStatusUnpublished
	if !tpl.IsUnpublished() {
		t.Fatal("IsUnpublished")
	}
}
