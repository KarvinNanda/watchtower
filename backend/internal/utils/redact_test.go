package utils

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactSecret(t *testing.T) {
	const secret = "123456:AAH-secret-token"
	err := fmt.Errorf("do request: Get \"https://api.telegram.org/bot%s/getUpdates\": %w", secret, context.DeadlineExceeded)

	got := RedactSecret(err, secret)

	require.Error(t, got)
	assert.NotContains(t, got.Error(), secret)
	assert.Contains(t, got.Error(), "bot<redacted>/getUpdates")
	assert.True(t, errors.Is(got, context.DeadlineExceeded), "the cause stays reachable for errors.Is")
}

func TestRedactSecret_NilAndEmpty(t *testing.T) {
	assert.NoError(t, RedactSecret(nil, "x"))
	err := errors.New("plain")
	assert.Equal(t, err, RedactSecret(err, ""), "an empty secret changes nothing")
	assert.Equal(t, err, RedactSecret(err, "absent"), "an error without the secret is returned as is")
}
