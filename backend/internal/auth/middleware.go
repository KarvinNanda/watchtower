package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	userIDContextKey            = "user_id"
	emailContextKey             = "email"
	preferredLanguageContextKey = "preferred_language"
)

// AuthMiddleware returns a Gin middleware that requires a valid "Bearer"
// JWT in the Authorization header, verified against svc, and stores the
// authenticated user's claims in the Gin context for downstream handlers.
func AuthMiddleware(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if header == "" || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "missing or malformed Authorization header",
			})
			return
		}

		claims, err := svc.ValidateToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": err.Error(),
			})
			return
		}

		c.Set(userIDContextKey, claims.UserID)
		c.Set(emailContextKey, claims.Email)
		c.Set(preferredLanguageContextKey, claims.PreferredLanguage)
		c.Next()
	}
}

// GetCurrentUserID extracts the authenticated user ID stored by
// AuthMiddleware from the Gin context.
func GetCurrentUserID(c *gin.Context) (uint64, error) {
	v, exists := c.Get(userIDContextKey)
	if !exists {
		return 0, errors.New("user_id not found in context")
	}
	id, ok := v.(uint64)
	if !ok {
		return 0, errors.New("user_id in context has unexpected type")
	}
	return id, nil
}
