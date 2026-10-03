package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

// rawCode has the shape createTemporalCode issues (32 random bytes, hex).
const rawCode = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// newUC2 builds an auth use case with a nil UoW factory (the temporal-code and
// single-statement paths under test do not start a transaction).
func newUC2(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		Config:   config.AuthConfig{TemporalCodeTTL: time.Hour},
	})
	return uc, repo
}

// fakeNotifier records the last notification.
type fakeNotifier struct {
	calls              int
	lastRecipientEmail string
	lastPayload        notificationTypes.NotificationPayload
	// marked: accounts whose invitation time was recorded (invite tests).
	marked []uuid.UUID
}

func (f *fakeNotifier) Notify(_ context.Context, _ uuid.UUID, n notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error {
	f.calls++
	f.lastPayload = n
	o := dispatchModel.ApplyNotifyOptions(opts)
	if o.Recipient != nil {
		f.lastRecipientEmail = o.Recipient.Email
	}
	return nil
}

func TestConsumeTemporalCode_RoundTrip(t *testing.T) {
	uc, repo := newUC2(t)
	uid := uuid.Must(uuid.NewV7())
	data, _ := json.Marshal(temporalCodeModel.TemporalPasswordResettingCodeData{UserID: uid})

	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{
		ID: uuid.Must(uuid.NewV7()), Code: auth.ExportHashTemporalCode(rawCode), Type: temporalCodeModel.PasswordResettingCodeType,
		Data: data, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	out, err := auth.ExportConsumeTemporalCode(uc, context.Background(), rawCode, temporalCodeModel.PasswordResettingCodeType)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	var got temporalCodeModel.TemporalPasswordResettingCodeData
	if err = json.Unmarshal(out, &got); err != nil || got.UserID != uid {
		t.Fatalf("data wrong: %v / %v", got, err)
	}
}

func TestConsumeTemporalCode_Expired(t *testing.T) {
	uc, repo := newUC2(t)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{
		Code: auth.ExportHashTemporalCode(rawCode), Type: temporalCodeModel.PasswordResettingCodeType, ExpiresAt: time.Now().Add(-time.Minute),
	}, nil)

	_, err := auth.ExportConsumeTemporalCode(uc, context.Background(), rawCode, temporalCodeModel.PasswordResettingCodeType)
	if !errors.Is(err, temporalCodeModel.ErrTemporalCodeExpired.Err()) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestConsumeTemporalCode_NotFound(t *testing.T) {
	uc, repo := newUC2(t)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{}, pgx.ErrNoRows)

	_, err := auth.ExportConsumeTemporalCode(uc, context.Background(), rawCode, temporalCodeModel.PasswordResettingCodeType)
	// Used-or-never-existed must be indistinguishable from an invalid code
	// (category A): a 404 here would leak whether a code was ever issued.
	if !errors.Is(err, temporalCodeModel.ErrTemporalCodeInvalidCode.Err()) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestConsumeTemporalCode_WrongType(t *testing.T) {
	uc, repo := newUC2(t)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{
		Code: auth.ExportHashTemporalCode(rawCode), Type: temporalCodeModel.PasswordResettingCodeType + 1, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)

	_, err := auth.ExportConsumeTemporalCode(uc, context.Background(), rawCode, temporalCodeModel.PasswordResettingCodeType)
	if !errors.Is(err, temporalCodeModel.ErrTemporalCodeInvalidCode.Err()) {
		t.Fatalf("want invalid, got %v", err)
	}
}

// L2: the NUL byte that used to reach Postgres (and fail as a 500) is a plain
// invalid code, and the database is never asked.
func TestConsumeTemporalCode_MalformedNeverHitsDatabase(t *testing.T) {
	uc, _ := newUC2(t)
	for _, bad := range []string{"", "AAAA", "raw", "\x00" + rawCode[1:], rawCode + "0", strings.ToUpper(rawCode)} {
		_, err := auth.ExportConsumeTemporalCode(uc, context.Background(), bad, temporalCodeModel.PasswordResettingCodeType)
		if !errors.Is(err, temporalCodeModel.ErrTemporalCodeInvalidCode.Err()) {
			t.Fatalf("%q: want invalid code, got %v", bad, err)
		}
	}
}

// L2: two parallel requests with one code — only the request whose DELETE
// removes the row may succeed.
func TestConsumeTemporalCode_LostRaceIsInvalid(t *testing.T) {
	uc, repo := newUC2(t)
	data, _ := json.Marshal(temporalCodeModel.TemporalPasswordResettingCodeData{UserID: uuid.Must(uuid.NewV7())})
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{
		ID: uuid.Must(uuid.NewV7()), Type: temporalCodeModel.PasswordResettingCodeType,
		Data: data, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	_, err := auth.ExportConsumeTemporalCode(uc, context.Background(), rawCode, temporalCodeModel.PasswordResettingCodeType)
	if !errors.Is(err, temporalCodeModel.ErrTemporalCodeInvalidCode.Err()) {
		t.Fatalf("want invalid code, got %v", err)
	}
}
