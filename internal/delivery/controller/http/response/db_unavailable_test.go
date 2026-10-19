package response_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/overload"
)

func serveStoredError(failure error, canceled bool) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var seen *gin.Context
	router.Use(response.WithErrorHandler)
	router.GET("/x", func(ctx *gin.Context) {
		seen = ctx
		response.AbortWithError(ctx, model.ErrPlatform.WithError(failure).WithMessage("Failed to get user for session").Err())
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if canceled {
		ctx, cancel := context.WithCancel(req.Context())
		cancel()
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, seen
}

func TestWithErrorHandlerAnswers503WhenTheDatabaseIsUnavailable(t *testing.T) {
	for name, failure := range map[string]error{
		"admin shutdown 57P01":     &pgconn.PgError{Code: "57P01", Message: "terminating connection due to administrator command"},
		"crash shutdown 57P02":     &pgconn.PgError{Code: "57P02"},
		"cannot connect now 57P03": &pgconn.PgError{Code: "57P03"},
		"connection failure 08006": &pgconn.PgError{Code: "08006"},
		"wrapped":                  fmt.Errorf("load: %w", &pgconn.PgError{Code: "57P01"}),
		"dial error":               &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		"closed pool":              errors.New("closed pool"),
		"conn closed":              errors.New("conn closed"),
	} {
		t.Run(name, func(t *testing.T) {
			rec, ctx := serveStoredError(failure, false)
			if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
				t.Fatalf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
			}
			if code := overloadBody(t, rec); code != 70433 {
				t.Fatalf("code=%d", code)
			}
			// A fault, not protection: the error journal must keep recording it.
			if response.IsOverload(ctx) {
				t.Fatal("a database outage must not be marked as overload")
			}
		})
	}
}

func TestWithErrorHandlerKeepsOtherDatabaseErrorsAs500(t *testing.T) {
	for name, failure := range map[string]error{
		"unique violation": &pgconn.PgError{Code: "23505"},
		"syntax error":     &pgconn.PgError{Code: "42601"},
		"plain":            errors.New("boom"),
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := serveStoredError(failure, false)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", rec.Code)
			}
		})
	}
}

func TestWithErrorHandlerKeepsTheBusyPoolAs503Overload(t *testing.T) {
	rec, ctx := serveStoredError(fmt.Errorf("%w: no connection", overload.ErrDBBusy), false)
	if rec.Code != http.StatusServiceUnavailable || overloadBody(t, rec) != 70432 || !response.IsOverload(ctx) {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestWithErrorHandlerAnswers499ToAClientThatLeft(t *testing.T) {
	rec, _ := serveStoredError(context.Canceled, true)
	if rec.Code != 499 || overloadBody(t, rec) != 70434 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestWithErrorHandlerDoesNotHideAFaultBehindACancellation(t *testing.T) {
	rec, _ := serveStoredError(errors.Join(context.Canceled, errors.New("database failed")), true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	// A cancellation while the client is still there is a dependency fault.
	rec, _ = serveStoredError(context.Canceled, false)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
}
