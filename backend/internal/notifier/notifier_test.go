package notifier

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestSendOnce_ErrorDoesNotContainToken(t *testing.T) {
	const token = "555555:AAH-notifier-secret"
	n := NewNotifier(token, "")
	n.httpClient = &http.Client{Transport: failingTransport{}}

	err := n.sendOnce(token, 1, "hi")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), token)
	assert.Contains(t, err.Error(), "<redacted>")
}
