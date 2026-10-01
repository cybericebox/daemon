package notificationModel_test

import (
	"testing"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

func TestTemplateStatus_Typed(t *testing.T) {
	var s notificationModel.TemplateStatus = notificationModel.TemplateStatusDraft
	if s != "draft" {
		t.Fatalf("draft constant = %q", s)
	}
	if notificationModel.TemplateStatusPublished != "published" ||
		notificationModel.TemplateStatusUnpublished != "unpublished" {
		t.Fatal("status constants drifted")
	}
}
