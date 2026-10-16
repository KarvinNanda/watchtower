package runs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/karvin-nanda/watchtower/internal/auth"
)

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TelegramLinkChecker reports whether userID has linked the Telegram chat
// that runType delivers to. Without it a run would succeed but send nothing.
type TelegramLinkChecker interface {
	TelegramLinked(ctx context.Context, userID uint64, runType string) (bool, error)
}

type SQLTelegramLinkChecker struct {
	DB *sql.DB
}

func (c SQLTelegramLinkChecker) TelegramLinked(ctx context.Context, userID uint64, runType string) (bool, error) {
	// The column comes from a fixed switch, never from user input.
	var query string
	switch runType {
	case TypeAsset, TypeAssetReport:
		query = `SELECT telegram_asset_chat_id IS NOT NULL FROM users WHERE id = ?`
	case TypeSentinel:
		query = `SELECT telegram_sentinel_chat_id IS NOT NULL FROM users WHERE id = ?`
	default:
		return false, fmt.Errorf("runs: no telegram chat for run type %q", runType)
	}
	var linked bool
	err := c.DB.QueryRowContext(ctx, query, userID).Scan(&linked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("runs: check telegram link: %w", err)
	}
	return linked, nil
}

// RegisterRoutes mounts POST /runs and GET /runs/:id on rg, which must
// already be behind auth.AuthMiddleware.
func RegisterRoutes(rg *gin.RouterGroup, store *Store, links TelegramLinkChecker) {
	rg.POST("/runs", createRunHandler(store, links))
	rg.GET("/runs/:id", getRunHandler(store))
}

type createRunRequest struct {
	Type string `json:"type"`
}

func createRunHandler(store *Store, links TelegramLinkChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}

		var req createRunRequest
		if err := c.ShouldBindJSON(&req); err != nil || !ValidType(req.Type) {
			fail(c, http.StatusBadRequest, "validation_error", `type must be "asset", "asset_report" or "sentinel"`)
			return
		}

		ctx := c.Request.Context()

		alive, err := store.SchedulerAlive(ctx)
		if err != nil {
			log.Printf("[ERROR] createRunHandler: %v", err)
			fail(c, http.StatusInternalServerError, "internal_error", "could not start the run")
			return
		}
		if !alive {
			fail(c, http.StatusServiceUnavailable, "scheduler_offline",
				"The scheduler is not running. Start it with: docker compose up -d")
			return
		}

		linked, err := links.TelegramLinked(ctx, userID, req.Type)
		if err != nil {
			log.Printf("[ERROR] createRunHandler: %v", err)
			fail(c, http.StatusInternalServerError, "internal_error", "could not start the run")
			return
		}
		if !linked {
			fail(c, http.StatusConflict, "telegram_not_linked",
				"Link your Telegram chat for this alert type first.")
			return
		}

		run, err := store.Enqueue(ctx, userID, req.Type)
		var rl *RateLimitedError
		if errors.As(err, &rl) {
			secs := int(math.Ceil(rl.RetryAfter.Seconds()))
			c.Header("Retry-After", strconv.Itoa(secs))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success":     false,
				"error":       "too_many_runs",
				"message":     fmt.Sprintf("Please wait %d seconds before running again.", secs),
				"retry_after": secs,
			})
			return
		}
		if err != nil {
			log.Printf("[ERROR] createRunHandler: %v", err)
			fail(c, http.StatusInternalServerError, "internal_error", "could not start the run")
			return
		}

		c.JSON(http.StatusAccepted, gin.H{"success": true, "data": run, "message": "run queued"})
	}
}

func getRunHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}

		id := c.Param("id")
		if !runIDPattern.MatchString(id) {
			fail(c, http.StatusNotFound, "not_found", "run not found")
			return
		}

		run, err := store.Get(c.Request.Context(), id, userID)
		if errors.Is(err, ErrNotFound) {
			fail(c, http.StatusNotFound, "not_found", "run not found")
			return
		}
		if err != nil {
			log.Printf("[ERROR] getRunHandler: %v", err)
			fail(c, http.StatusInternalServerError, "internal_error", "could not load the run")
			return
		}

		c.JSON(http.StatusOK, gin.H{"success": true, "data": run, "message": "run retrieved"})
	}
}

func fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"success": false, "error": code, "message": message})
}
