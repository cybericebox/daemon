package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendPostsToTheBotEndpoint(t *testing.T) {
	var path string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	require.NoError(t, New("123:SECRET").WithBaseURL(srv.URL).Send(context.Background(), "-100", "hello"))
	assert.Equal(t, "/bot123:SECRET/sendMessage", path)
	assert.Equal(t, "-100", got["chat_id"])
	assert.Equal(t, "hello", got["text"])
}

func TestSendMapsForbiddenAndMissingChat(t *testing.T) {
	status, desc := http.StatusForbidden, "Forbidden: bot was blocked by the user"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": desc})
	}))
	defer srv.Close()
	c := New("123:SECRET").WithBaseURL(srv.URL)

	assert.ErrorIs(t, c.Send(context.Background(), "1", "x"), ErrForbidden)
	status, desc = http.StatusBadRequest, "Bad Request: chat not found"
	assert.ErrorIs(t, c.Send(context.Background(), "1", "x"), ErrChatNotFound)
}

func TestSendErrorsNeverCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused

	err := New("123456789:SECRETSECRETSECRETSECRETSECRETSEC").WithBaseURL(url).Send(context.Background(), "1", "x")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
}

func TestDisabledClient(t *testing.T) {
	assert.False(t, New("").Enabled())
	assert.ErrorIs(t, New("").Send(context.Background(), "1", "x"), ErrDisabled)
}

func TestValidChatID(t *testing.T) {
	for _, ok := range []string{"123456", "-1001234567890", "@mychannel"} {
		assert.True(t, ValidChatID(ok), ok)
	}
	for _, bad := range []string{"", "abc", "12 3", "@ab", "1;2"} {
		assert.False(t, ValidChatID(bad), bad)
	}
}
