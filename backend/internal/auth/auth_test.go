package auth_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/karvin-nanda/watchtower/internal/auth"
)

// newLoginTestContext builds a *gin.Context backed by a real recorder/
// request, since Login now takes *gin.Context directly (to set the auth
// cookie on the response) rather than a bare context.Context.
func newLoginTestContext(remoteAddr string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	c.Request.RemoteAddr = remoteAddr
	return c, w
}

// readSetCookies parses every Set-Cookie header written to w into a
// name-keyed map, so tests can assert on a specific cookie's attributes
// (HttpOnly, Secure, Value) without string-parsing the raw header.
func readSetCookies(w *httptest.ResponseRecorder) map[string]*http.Cookie {
	result := make(map[string]*http.Cookie)
	for _, c := range w.Result().Cookies() {
		result[c.Name] = c
	}
	return result
}

const testJWTSecret = "test-secret-do-not-use-in-prod"

// newMockService builds an auth.Service backed by a sqlmock database, so
// tests never touch a real MySQL connection.
func newMockService(t *testing.T) (*auth.Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return auth.NewService(db, testJWTSecret, 1), mock
}

// bcryptHashMatcher asserts an insert argument is a bcrypt hash of a known
// plaintext, rather than the plaintext itself — proving Register never
// stores a raw password.
type bcryptHashMatcher struct{ plaintext string }

func (m bcryptHashMatcher) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok || s == m.plaintext {
		return false
	}
	if !strings.HasPrefix(s, "$2a$") && !strings.HasPrefix(s, "$2b$") {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(s), []byte(m.plaintext)) == nil
}

func expectSuccessfulRegister(mock sqlmock.Sqlmock, email, password string) {
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO users`).
		WithArgs(email, bcryptHashMatcher{plaintext: password}).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO user_profiles`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
}

func TestRegister_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	const plaintext = "Valid1Pass!"
	expectSuccessfulRegister(mock, "test@example.com", plaintext)

	err := svc.Register(context.Background(), "Test@Example.COM", plaintext)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet(),
		"the INSERT must have received a lowercased email and a bcrypt hash, not the raw input")
}

func TestRegister_DuplicateEmail(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	const email = "dup@example.com"
	const password = "Valid1Pass!"

	expectSuccessfulRegister(mock, email, password)
	require.NoError(t, svc.Register(context.Background(), email, password))

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO users`).
		WithArgs(email, bcryptHashMatcher{plaintext: password}).
		WillReturnError(&mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry"})
	mock.ExpectRollback()

	err := svc.Register(context.Background(), email, password)

	assert.ErrorIs(t, err, auth.ErrEmailTaken)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRegister_WeakPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"too_short", "short", auth.ErrPasswordTooShort},
		{"no_uppercase", "alllowercase1!", auth.ErrPasswordTooWeak},
		{"no_lowercase", "ALLUPPERCASE1!", auth.ErrPasswordTooWeak},
		{"no_special_char", "NoSpecialChar1", auth.ErrPasswordTooWeak},
		{"no_number", "NoNumber!", auth.ErrPasswordTooWeak},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, _ := newMockService(t)
			// No DB expectations are set: every case here must fail
			// validation before Register ever touches the database.
			err := svc.Register(context.Background(), "someone@example.com", tc.password)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}

	t.Run("valid_password_succeeds", func(t *testing.T) {
		t.Parallel()
		svc, mock := newMockService(t)
		expectSuccessfulRegister(mock, "someone@example.com", "Valid1Pass!")
		err := svc.Register(context.Background(), "someone@example.com", "Valid1Pass!")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestRegister_InvalidEmail(t *testing.T) {
	t.Parallel()

	invalidCases := []string{"notanemail", "@nodomain.com", "no@"}
	for _, email := range invalidCases {
		t.Run(email, func(t *testing.T) {
			t.Parallel()
			svc, _ := newMockService(t)
			err := svc.Register(context.Background(), email, "Valid1Pass!")
			assert.ErrorIs(t, err, auth.ErrInvalidEmail)
		})
	}

	t.Run("valid@test.com", func(t *testing.T) {
		t.Parallel()
		svc, mock := newMockService(t)
		expectSuccessfulRegister(mock, "valid@test.com", "Valid1Pass!")
		err := svc.Register(context.Background(), "valid@test.com", "Valid1Pass!")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

var userColumns = []string{
	"id", "email", "password_hash", "telegram_asset_chat_id", "telegram_sentinel_chat_id",
	"alert_cooldown_hours", "preferred_language", "is_active", "created_at", "updated_at",
}

func userRow(id uint64, email, passwordHash string) *sqlmock.Rows {
	return sqlmock.NewRows(userColumns).
		AddRow(id, email, passwordHash, nil, nil, 4, "id", true, time.Now(), time.Now())
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	const email = "login@example.com"
	const password = "Valid1Pass!"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs(email).
		WillReturnRows(userRow(42, email, string(hash)))

	c, w := newLoginTestContext("203.0.113.1:12345")
	u, err := svc.Login(c, email, password, false)
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, uint64(42), u.ID)
	assert.Equal(t, email, u.Email)

	cookies := readSetCookies(w)
	require.Contains(t, cookies, "watchtower_token", "Login must set the watchtower_token cookie")
	tokenCookie := cookies["watchtower_token"]
	assert.True(t, tokenCookie.HttpOnly, "auth cookie must be HttpOnly so frontend JS can never read it")
	assert.False(t, tokenCookie.Secure, "Secure must be false when isProduction=false")
	assert.NotEmpty(t, tokenCookie.Value)

	claims, err := svc.ValidateToken(tokenCookie.Value)
	require.NoError(t, err, "the token set as a cookie by Login must be accepted by ValidateToken")
	assert.Equal(t, uint64(42), claims.UserID)
	assert.Equal(t, email, claims.Email)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogin_SetsSecureCookieInProduction(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	const email = "prod-login@example.com"
	const password = "Valid1Pass!"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs(email).
		WillReturnRows(userRow(7, email, string(hash)))

	c, w := newLoginTestContext("203.0.113.9:12345")
	_, err = svc.Login(c, email, password, true)
	require.NoError(t, err)

	cookies := readSetCookies(w)
	require.Contains(t, cookies, "watchtower_token")
	assert.True(t, cookies["watchtower_token"].Secure, "Secure must be true when isProduction=true")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogin_WrongPassword(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	const email = "wrongpass@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Valid1Pass!"), bcrypt.DefaultCost)
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs(email).
		WillReturnRows(userRow(1, email, string(hash)))

	c, w := newLoginTestContext("203.0.113.2:12345")
	u, err := svc.Login(c, email, "WrongPassword1!", false)

	assert.Nil(t, u)
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	assert.Equal(t, "invalid email or password", err.Error())
	assert.Empty(t, w.Result().Cookies(), "no cookie should be set on a failed login")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogin_UserNotFound(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs("nobody@example.com").
		WillReturnError(sql.ErrNoRows)

	c, w := newLoginTestContext("203.0.113.3:12345")
	u, notFoundErr := svc.Login(c, "nobody@example.com", "WrongPassword1!", false)
	assert.Nil(t, u)
	require.ErrorIs(t, notFoundErr, auth.ErrInvalidCredentials)

	// Re-derive the "wrong password" error message the same way
	// TestLogin_WrongPassword does, to prove the two paths are
	// byte-for-byte identical — the whole point of this check is
	// preventing user enumeration via a differing error message.
	assert.Equal(t, "invalid email or password", notFoundErr.Error())
	assert.Empty(t, w.Result().Cookies(), "no cookie should be set when the account doesn't exist")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateToken_Expired(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)

	claims := auth.Claims{
		UserID: 1,
		Email:  "expired@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Second)),
			Subject:   "1",
			Issuer:    "watchtower",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testJWTSecret))
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	_, err = svc.ValidateToken(signed)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)

	claims := auth.Claims{
		UserID: 1,
		Email:  "tampered@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Subject:   "1",
			Issuer:    "watchtower",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// Signed with a different secret than the service validates against.
	signed, err := token.SignedString([]byte("a-completely-different-secret"))
	require.NoError(t, err)

	_, err = svc.ValidateToken(signed)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_AlgorithmNone(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)

	claims := auth.Claims{
		UserID: 1,
		Email:  "none-alg@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Subject:   "1",
			Issuer:    "watchtower",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = svc.ValidateToken(signed)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestGetUserByID_Found(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs(uint64(5)).
		WillReturnRows(userRow(5, "byid@example.com", "$2a$12$hash"))

	u, err := svc.GetUserByID(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, "byid@example.com", u.Email)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserByID_NotFound(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs(uint64(404)).
		WillReturnError(sql.ErrNoRows)

	_, err := svc.GetUserByID(context.Background(), 404)
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTelegramChatID_InvalidBotType(t *testing.T) {
	t.Parallel()
	svc, _ := newMockService(t)

	err := svc.UpdateTelegramChatID(context.Background(), 1, "carrier-pigeon", 12345)
	assert.ErrorIs(t, err, auth.ErrInvalidBotType)
}

func TestUpdateTelegramChatID_Success(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	mock.ExpectExec(`UPDATE users SET telegram_asset_chat_id`).
		WithArgs(int64(555), uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateTelegramChatID(context.Background(), 1, "asset", 555)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTelegramChatID_UserNotFound(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	mock.ExpectExec(`UPDATE users SET telegram_sentinel_chat_id`).
		WithArgs(int64(555), uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := svc.UpdateTelegramChatID(context.Background(), 999, "sentinel", 555)
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHashPassword_ComparePassword(t *testing.T) {
	t.Parallel()

	hash, err := auth.HashPassword("Valid1Pass!")
	require.NoError(t, err)
	assert.NotEqual(t, "Valid1Pass!", hash)

	assert.True(t, auth.ComparePassword(hash, "Valid1Pass!"))
	assert.False(t, auth.ComparePassword(hash, "WrongPassword!"))
}
