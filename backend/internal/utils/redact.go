package utils

import "strings"

// redactedError prints a message with a secret removed and still unwraps
// to the original error, so errors.Is and errors.As keep working.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// RedactSecret returns err with every occurrence of secret in its message
// replaced by "<redacted>". Use it on errors from requests whose URL holds a
// secret (Telegram puts the bot token in the path): net/http quotes the full
// URL in its errors, and those errors are logged. nil, an empty secret, or
// an error that does not contain the secret is returned unchanged.
// Unwrap still returns the original error, which holds the secret: never
// print an unwrapped cause (for example a *url.Error's URL) from these.
func RedactSecret(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return &redactedError{msg: strings.ReplaceAll(err.Error(), secret, "<redacted>"), err: err}
}
