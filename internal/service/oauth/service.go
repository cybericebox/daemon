package oauthService

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/lib/pkg/libError"
	"github.com/cybericebox/lib/pkg/token"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"io"
	"time"
)

const (
	issuer = "oauth"

	oauthGoogleUrlAPI  = "https://www.googleapis.com/oauth2/v2/userinfo?access_token="
	oauthGoogleSubject = "google-user"
)

type (
	OAuthService struct {
		googleConfig *oauth2.Config
		tokenManager ITokenManager
	}

	ITokenManager interface {
		NewBase64Token(subject interface{}, ttl ...time.Duration) (string, error)
		ParseBase64Token(token string) (interface{}, error)
	}

	Dependencies struct {
		Config *config.OAuthConfig
	}
)

func NewService(deps Dependencies) *OAuthService {
	manager, err := token.NewBase64TokenManager(token.Base64TokenDependencies{
		SigningKey: deps.Config.StateSignature,
		Issuer:     issuer,
		TTL:        deps.Config.StateTTL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create token manager for oauth service")
	}

	return &OAuthService{
		googleConfig: &oauth2.Config{
			ClientID:     deps.Config.Google.ClientID,
			ClientSecret: deps.Config.Google.ClientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  fmt.Sprintf(deps.Config.RedirectURLTemplate, "google"),
			Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
		},
		tokenManager: manager,
	}
}

func (s *OAuthService) GetGoogleLoginURL() (string, error) {
	randomState, err := s.tokenManager.NewBase64Token(oauthGoogleSubject)
	if err != nil {
		return "", authModel.ErrAuth.WithError(err).WithMessage("Failed to generate state").Err()
	}
	return s.googleConfig.AuthCodeURL(randomState), nil
}

func (s *OAuthService) GetGoogleUser(ctx context.Context, code, state string) (*userModel.User, error) {
	subject, err := s.tokenManager.ParseBase64Token(state)
	if err != nil {
		if errors.Is(err, libError.ErrTokenInvalidJWTToken.Err()) {
			return nil, authModel.ErrAuthInvalidOAuth2State.Err()
		}
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to parse state").Err()
	}
	if subject != oauthGoogleSubject {
		return nil, authModel.ErrAuthInvalidOAuth2State.Err()
	}

	tokens, err := s.googleConfig.Exchange(ctx, code)
	if err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to exchange code for tokens").Err()
	}

	client := s.googleConfig.Client(ctx, tokens)

	response, err := client.Get(oauthGoogleUrlAPI + tokens.AccessToken)
	if err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to get google user").Err()
	}

	defer func() {
		if err = response.Body.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close response body")
		}
	}()

	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to read response body").Err()
	}

	var GoogleUserRes map[string]interface{}

	if err = json.Unmarshal(content, &GoogleUserRes); err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to unmarshal google user response").Err()
	}

	return &userModel.User{
		GoogleID: GoogleUserRes["id"].(string),
		Email:    GoogleUserRes["email"].(string),
		Name:     GoogleUserRes["name"].(string),
		Picture:  GoogleUserRes["picture"].(string),
	}, nil
}
