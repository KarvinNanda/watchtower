package telegram

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingTransport fails every request without touching the network; the
// http.Client then returns a *url.Error that quotes the full URL.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

const testToken = "987654:AAH-test-secret"

func TestGetUpdates_ErrorDoesNotContainToken(t *testing.T) {
	old := pollingHTTPClient
	pollingHTTPClient = &http.Client{Transport: failingTransport{}}
	t.Cleanup(func() { pollingHTTPClient = old })

	h := NewBotHandler(testToken, "")
	_, err := h.getUpdates(context.Background(), testToken, 1)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), testToken)
	assert.Contains(t, err.Error(), "<redacted>")
}

func TestSendMessage_ErrorDoesNotContainToken(t *testing.T) {
	h := NewBotHandler(testToken, "")
	h.httpClient = &http.Client{Transport: failingTransport{}}

	err := h.sendMessage(testToken, 1, "hi")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), testToken)
	assert.Contains(t, err.Error(), "<redacted>")
}
