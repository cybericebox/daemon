package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"io"
	"strings"
)

const (
	randomStateLen    = 10
	oauthGoogleUrlAPI = "https://www.googleapis.com/oauth2/v2/userinfo?access_token="
)

var (
	ErrInvalidState = appError.NewError().WithCode(appError.CodeInvalidInput.WithMessage("invalid state"))
)

type (
	OAuthService struct {
		googleConfig *oauth2.Config
		randomState  string
	}

	Dependencies struct {
		Config *config.OAuthConfig
	}
)

func NewOAuthService(deps Dependencies) *OAuthService {
	r := make([]byte, randomStateLen)
	_, err := rand.Read(r)
	if err != nil {
		log.Fatal().Err(err).Msg("Creating google service")
		return nil
	}
	return &OAuthService{
		googleConfig: &oauth2.Config{
			ClientID:     deps.Config.Google.ClientID,
			ClientSecret: deps.Config.Google.ClientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  fmt.Sprintf(deps.Config.RedirectURLTemplate, "google"),
			Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
		},
		randomState: base64.StdEncoding.EncodeToString(r),
	}
}

func (s *OAuthService) GetGoogleLoginURL() string {
	return s.googleConfig.AuthCodeURL(s.randomState)
}

func (s *OAuthService) GetGoogleUser(ctx context.Context, code, state string) (*model.User, error) {
	if strings.Compare(state, s.randomState) != 0 {
		return nil, ErrInvalidState
	}

	tokens, err := s.googleConfig.Exchange(ctx, code)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to exchange code for tokens")
	}

	client := s.googleConfig.Client(ctx, tokens)

	response, err := client.Get(oauthGoogleUrlAPI + tokens.AccessToken)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get google user")
	}

	defer func() {
		if err = response.Body.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close response body")
		}
	}()

	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to read response body")
	}

	var GoogleUserRes map[string]interface{}

	if err = json.Unmarshal(content, &GoogleUserRes); err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to unmarshal google user response")
	}

	return &model.User{
		GoogleID: GoogleUserRes["id"].(string),
		Email:    GoogleUserRes["email"].(string),
		Name:     GoogleUserRes["name"].(string),
		Picture:  GoogleUserRes["picture"].(string),
	}, nil
}
