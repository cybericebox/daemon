package inAppModel_test

import (
	"testing"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
)

func TestNewDraft_DomainDefaults(t *testing.T) {
	by := uuid.Must(uuid.NewV7())
	tpl := inAppModel.NewDraft(inAppModel.CreateTemplateInput{
		NotificationType: "user_invitation",
		Title:            "t",
		Body:             "b",
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
}

func TestInAppTemplate_StatusPredicates(t *testing.T) {
	tpl := inAppModel.InAppTemplate{Status: notificationModel.TemplateStatusDraft}
	if !tpl.IsDraft() || tpl.IsPublished() {
		t.Fatalf("predicates wrong for draft: %+v", tpl.Status)
	}
}
