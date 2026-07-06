package harbor

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientSucceedsWhenHarborIsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2.0/ping" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Pong"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL + "/api/v2.0")
	require.NoError(t, err)

	client, err := NewClient(Config{URL: srvURL, Timeout: 5 * time.Second}, "admin", "password")

	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewClientErrorsWhenHarborUnreachable(t *testing.T) {
	srvURL, err := url.Parse("http://127.0.0.1:1/api/v2.0")
	require.NoError(t, err)

	_, err = NewClient(Config{URL: srvURL, Timeout: 5 * time.Second}, "admin", "password")
	assert.Error(t, err)
}

func TestNewClientErrorsWhenHarborReturnsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL + "/api/v2.0")
	require.NoError(t, err)

	_, err = NewClient(Config{URL: srvURL, Timeout: 5 * time.Second}, "admin", "password")
	assert.Error(t, err)
}
