package auth_test

import (
	"context"
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

func doRequest(r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)
	r := newProtectedRouter(svc)

	rec := doRequest(r, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_MalformedHeader(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)
	r := newProtectedRouter(svc)

	rec := doRequest(r, "NotBearer sometoken")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	t.Parallel()
	svc := auth.NewService(nil, testJWTSecret, 1)
	r := newProtectedRouter(svc)

	rec := doRequest(r, "Bearer not-a-real-token")
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

	token, err := svc.Login(context.Background(), email, "Valid1Pass!", "127.0.0.1")
	require.NoError(t, err)

	r := newProtectedRouter(svc)
	rec := doRequest(r, "Bearer "+token)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "user:9", rec.Body.String())
}
