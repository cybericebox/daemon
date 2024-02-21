package auth

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/gin-gonic/gin"
)

type (
	Handler struct {
		service Service
	}

	Service interface {
		signService
		signUpService
		emailService
		passwordService
		googleService
	}
)

func NewAuthAPIHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	authApi := router.Group("/auth")
	{
		authApi.POST("/sign-in", protection.ProtectAPIRouteWithRecaptcha(protection.SignIn), h.signIn)
		authApi.GET("/sign-out", protection.ProtectRouteWithAuthentication, h.signOut)

		// two-step sign up with email confirmation
		authApi.POST("/sign-up", protection.ProtectAPIRouteWithRecaptcha(protection.SignUp), h.signUp)
		authApi.POST("/sign-up/:token", h.signUpContinue)

		password := authApi.Group("/password")
		{
			password.POST("/forgot", protection.ProtectAPIRouteWithRecaptcha(protection.ForgotPassword), h.forgotPassword)
			password.POST("/reset/:token", h.resetPassword)
		}

		email := authApi.Group("/email")
		{
			email.POST("/send-confirmation", protection.ProtectRouteWithAuthentication, h.sendEmailConfirmation)
			email.POST("/confirm/:token", h.confirmEmail)
		}

		//with oauth
		oauth := authApi.Group("/oauth")
		{
			google := oauth.Group("/google")
			{
				google.GET("", h.googleOAuthRedirect)
				google.GET("/callback", h.googleOAuthCallback)
			}
		}
	}
}
