package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	userIDContextKey            = "user_id"
	emailContextKey             = "email"
	preferredLanguageContextKey = "preferred_language"
)

// AuthMiddleware returns a Gin middleware that requires a valid JWT in the
// watchtower_token httpOnly cookie (set by Service.Login), verified against
// svc, and stores the authenticated user's claims in the Gin context for
// downstream handlers.
func AuthMiddleware(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, err := c.Cookie(authCookieName)
		if err != nil || tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "unauthorized",
				"message": "authentication required",
			})
			return
		}

		claims, err := svc.ValidateToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
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
