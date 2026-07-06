package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/karvin-nanda/watchtower/internal/auth"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newProtectedRouter(svc *auth.Service) *gin.Engine {
	r := gin.New()
	r.GET("/protected", auth.AuthMiddleware(svc), func(c *gin.Context) {
		id, err := auth.GetCurrentUserID(c)
		if err != nil {
			c.String(http.StatusInternalServerError, "no user id")
			return
		}
		c.String(http.StatusOK, "user:%d", id)
	})
	return r
}

// doRequest calls the protected route with cookieValue set as the
// watchtower_token cookie (AuthMiddleware now reads the token from a
// cookie, not an Authorization header) — an empty cookieValue sends no
// cookie at all.
func doRequest(r *gin.Engine, cookieValue string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: "watchtower_token", Value: cookieValue})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthMiddleware_MissingCookie(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)
	r := newProtectedRouter(svc)

	rec := doRequest(r, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_EmptyCookie(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)
	r := newProtectedRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "watchtower_token", Value: ""})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)
	r := newProtectedRouter(svc)

	rec := doRequest(r, "not-a-real-token")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	t.Parallel()
	svc, mock := newMockService(t)

	const email = "middleware@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Valid1Pass!"), bcrypt.DefaultCost)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT (.+) FROM users`).
		WithArgs(email).
		WillReturnRows(userRow(9, email, string(hash)))

	loginCtx, loginRec := newLoginTestContext("127.0.0.1:12345")
	_, err = svc.Login(loginCtx, email, "Valid1Pass!", false)
	require.NoError(t, err)

	cookies := readSetCookies(loginRec)
	require.Contains(t, cookies, "watchtower_token")
	token := cookies["watchtower_token"].Value

	r := newProtectedRouter(svc)
	rec := doRequest(r, token)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user:9", rec.Body.String())
}
