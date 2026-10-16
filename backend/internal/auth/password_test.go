package auth_test

import (
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/karvin-nanda/watchtower/internal/auth"
)

const oldPassword = "Old1Pass!"

func hashOf(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return string(h)
}

// loginToken logs user 9 in at the given token version and returns the cookie.
func loginToken(t *testing.T, svc *auth.Service, mock sqlmock.Sqlmock, version uint32) string {
	t.Helper()
	mock.ExpectQuery(`SELECT (.+) FROM users`).WithArgs("me@example.com").
		WillReturnRows(userRowVersion(9, "me@example.com", hashOf(t, oldPassword), version))
	c, w := newLoginTestContext("127.0.0.1:1")
	_, err := svc.Login(c, "me@example.com", oldPassword, false)
	require.NoError(t, err)
	return readSetCookies(w)["watchtower_token"].Value
}

func TestLogin_TokenCarriesTheVersion(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)
	token := loginToken(t, svc, mock, 3)

	claims, err := svc.ValidateToken(token)

	require.NoError(t, err)
	assert.Equal(t, uint32(3), claims.TokenVersion)
}

func TestChangePassword_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)
	mock.ExpectQuery(`SELECT (.+) FROM users WHERE id = \?`).WithArgs(uint64(9)).
		WillReturnRows(userRowVersion(9, "me@example.com", hashOf(t, oldPassword), 2))
	mock.ExpectExec(`UPDATE users SET password_hash = \?, token_version = token_version \+ 1 WHERE id = \? AND token_version = \?`).
		WithArgs(bcryptHashMatcher{plaintext: "New2Pass@"}, uint64(9), uint32(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	c, w := newLoginTestContext("127.0.0.1:1")

	err := svc.ChangePassword(c, 9, oldPassword, "New2Pass@", false)

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	// This session stays signed in with a token for the new version.
	cookie := readSetCookies(w)["watchtower_token"]
	require.NotNil(t, cookie)
	claims, err := svc.ValidateToken(cookie.Value)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), claims.TokenVersion)
}

func TestChangePassword_Rejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		current string
		next    string
		want    error
	}{
		{"wrong current password", "Wrong1Pass!", "New2Pass@", auth.ErrWrongPassword},
		{"new password too short", oldPassword, "N2p@", auth.ErrPasswordTooShort},
		{"new password too weak", oldPassword, "newpassword", auth.ErrPasswordTooWeak},
		{"same password", oldPassword, oldPassword, auth.ErrSamePassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, mock := newMockService(t)
			mock.ExpectQuery(`SELECT (.+) FROM users WHERE id = \?`).WithArgs(uint64(9)).
				WillReturnRows(userRowVersion(9, "me@example.com", hashOf(t, oldPassword), 0))
			c, w := newLoginTestContext("127.0.0.1:1")

			err := svc.ChangePassword(c, 9, tc.current, tc.next, false)

			assert.ErrorIs(t, err, tc.want)
			assert.NoError(t, mock.ExpectationsWereMet(), "nothing is written")
			assert.NotContains(t, readSetCookies(w), "watchtower_token", "no new session")
		})
	}
}

// A changed password logs out every other session: their tokens carry the
// old version.
func TestAuthMiddleware_RejectsATokenFromBeforeAPasswordChange(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)
	token := loginToken(t, svc, mock, 0)
	mock.ExpectQuery(`SELECT token_version FROM users WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"token_version"}).AddRow(1))

	rec := doRequest(newProtectedRouter(svc), token)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthMiddleware_RejectsAnInactiveOrDeletedUser(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)
	token := loginToken(t, svc, mock, 0)
	mock.ExpectQuery(`SELECT token_version FROM users`).WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"token_version"}))

	rec := doRequest(newProtectedRouter(svc), token)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
