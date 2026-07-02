// Command api serves the WatchTower REST API: user auth, asset/keyword
// subscription management, cached market data lookups, and (depending on
// TELEGRAM_MODE) either Telegram webhook routes or long-polling goroutines
// for the /start bot handshake.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/karvin-nanda/watchtower/internal/auth"
	"github.com/karvin-nanda/watchtower/internal/config"
	"github.com/karvin-nanda/watchtower/internal/db"
	"github.com/karvin-nanda/watchtower/internal/middleware"
	"github.com/karvin-nanda/watchtower/internal/telegram"
	"github.com/karvin-nanda/watchtower/internal/user"
)

// maxRequestBodyBytes caps every incoming request body at 1MB.
const maxRequestBodyBytes = 1 << 20

// Rate limits, per middleware.RateLimiter's (maxRequests, windowSeconds)
// signature.
const (
	authRateLimitMaxRequests = 10
	authRateLimitWindowSecs  = 60
	apiRateLimitMaxRequests  = 100
	apiRateLimitWindowSecs   = 60
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("[ERROR] main: load config: %v", err)
	}

	database, err := db.New(cfg)
	if err != nil {
		log.Fatalf("[ERROR] main: connect to database: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		log.Fatalf("[ERROR] main: run migrations: %v", err)
	}

	authService := auth.NewService(database.SQL, cfg.JWT.Secret, cfg.JWT.ExpiryHours)
	userService := user.NewUserService(database.SQL, cfg.Limits.MaxUniqueSymbols)
	botHandler := telegram.NewBotHandler(cfg.Telegram.AssetBotToken, cfg.Telegram.SentinelBotToken)

	if cfg.Server.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	// Recovery must be registered before anything that could panic, since
	// it catches panics via a deferred recover() wrapped around its own
	// c.Next() call — middleware registered ahead of it would panic
	// uncaught. Logger and the security/CORS/size-limit/SQLi middleware are
	// registered after it for that reason, even though the one-shot spec
	// listed Logger/Recovery last.
	router.Use(gin.Recovery())
	router.Use(gin.Logger())
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.CORSMiddleware(cfg.Server.Env, cfg.Server.FrontendURL))
	router.Use(middleware.RequestSizeLimit(maxRequestBodyBytes))
	router.Use(middleware.SQLInjectionGuard())

	registerRoutes(router, database, authService, userService)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	if cfg.Telegram.Mode == "webhook" {
		router.POST("/webhook/telegram/asset", botHandler.WebhookHandler("asset"))
		router.POST("/webhook/telegram/sentinel", botHandler.WebhookHandler("sentinel"))
		log.Println("[INFO] main: telegram webhook routes registered at /webhook/telegram/{asset,sentinel}")
	} else {
		log.Println("[INFO] main: starting telegram long-polling (TELEGRAM_MODE=polling)")

		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := botHandler.StartPolling(ctx, "asset"); err != nil {
				log.Printf("[ERROR] asset polling: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := botHandler.StartPolling(ctx, "sentinel"); err != nil {
				log.Printf("[ERROR] sentinel polling: %v", err)
			}
		}()
	}

	addr := ":" + strconv.Itoa(cfg.Server.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("api: listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[ERROR] main: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[INFO] main: shutdown signal received, stopping server and telegram polling")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[ERROR] main: server shutdown: %v", err)
	}

	wg.Wait()
	log.Println("[INFO] main: api stopped gracefully")
}

func registerRoutes(router *gin.Engine, database *db.DB, authService *auth.Service, userService *user.UserService) {
	router.GET("/health", healthHandler(database))

	api := router.Group("/api")

	authGroup := api.Group("/auth")
	authGroup.Use(middleware.RateLimiter(authRateLimitMaxRequests, authRateLimitWindowSecs))
	authGroup.POST("/register", registerHandler(authService))
	authGroup.POST("/login", loginHandler(authService))
	authGroup.GET("/me", auth.AuthMiddleware(authService), meHandler(authService))

	protected := api.Group("/")
	protected.Use(middleware.RateLimiter(apiRateLimitMaxRequests, apiRateLimitWindowSecs))
	protected.Use(auth.AuthMiddleware(authService))

	protected.GET("/user/profile", getUserProfileHandler(userService))
	protected.PUT("/user/profile", updateUserProfileHandler(userService))

	protected.GET("/subscriptions/assets", listAssetSubsHandler(userService))
	protected.POST("/subscriptions/assets", createAssetSubHandler(userService))
	protected.PUT("/subscriptions/assets/:id", updateAssetSubHandler(userService))
	protected.DELETE("/subscriptions/assets/:id", deleteAssetSubHandler(userService))

	protected.GET("/subscriptions/keywords", listKeywordSubsHandler(userService))
	protected.POST("/subscriptions/keywords", createKeywordSubHandler(userService))
	protected.DELETE("/subscriptions/keywords/:id", deleteKeywordSubHandler(userService))

	protected.GET("/notifications", notificationHistoryHandler(userService))

	protected.GET("/market/snapshot", marketSnapshotHandler(userService))
	protected.GET("/market/:symbol", marketQuoteHandler(database.SQL))

	protected.GET("/dashboard", dashboardHandler(userService))
}

// respondData writes a successful {"data": ..., "message": ...} response.
func respondData(c *gin.Context, status int, data interface{}, message string) {
	c.JSON(status, gin.H{"data": data, "message": message})
}

// respondError writes a {"error": <code>, "message": ...} response.
func respondError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": code, "message": message})
}

// respondSuccess writes a {"success": true, "data": ..., "message": ...}
// response, used by the user/subscription/notification/dashboard endpoints.
func respondSuccess(c *gin.Context, status int, data interface{}, message string) {
	c.JSON(status, gin.H{"success": true, "data": data, "message": message})
}

// respondFail writes a {"success": false, "error": <code>, "message": ...}
// response.
func respondFail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"success": false, "error": code, "message": message})
}

// respondPaginated writes a {"success": true, "data": ..., "meta": {...}}
// response for list endpoints that support limit/offset pagination.
func respondPaginated(c *gin.Context, status int, data interface{}, total, limit, offset int) {
	c.JSON(status, gin.H{
		"success": true,
		"data":    data,
		"meta": gin.H{
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
	})
}

func healthHandler(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		dbStatus := "ok"
		if err := database.SQL.PingContext(ctx); err != nil {
			log.Printf("[ERROR] healthHandler: ping mysql: %v", err)
			dbStatus = "error"
		}

		redisStatus := "ok"
		if err := database.Redis.Ping(ctx).Err(); err != nil {
			log.Printf("[ERROR] healthHandler: ping redis: %v", err)
			redisStatus = "error"
		}

		status := "ok"
		httpStatus := http.StatusOK
		if dbStatus != "ok" || redisStatus != "ok" {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		c.JSON(httpStatus, gin.H{"status": status, "db": dbStatus, "redis": redisStatus})
	}
}

// userResponse is the safe, external representation of auth.User — it
// never includes the password hash.
type userResponse struct {
	ID                     uint64    `json:"id"`
	Email                  string    `json:"email"`
	TelegramAssetChatID    *int64    `json:"telegram_asset_chat_id,omitempty"`
	TelegramSentinelChatID *int64    `json:"telegram_sentinel_chat_id,omitempty"`
	AlertCooldownHours     int       `json:"alert_cooldown_hours"`
	PreferredLanguage      string    `json:"preferred_language"`
	IsActive               bool      `json:"is_active"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func toUserResponse(u *auth.User) userResponse {
	return userResponse{
		ID:                     u.ID,
		Email:                  u.Email,
		TelegramAssetChatID:    u.TelegramAssetChatID,
		TelegramSentinelChatID: u.TelegramSentinelChatID,
		AlertCooldownHours:     u.AlertCooldownHours,
		PreferredLanguage:      u.PreferredLanguage,
		IsActive:               u.IsActive,
		CreatedAt:              u.CreatedAt,
		UpdatedAt:              u.UpdatedAt,
	}
}

type registerRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func registerHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "validation_error", err.Error())
			return
		}

		err := authService.Register(c.Request.Context(), req.Email, req.Password)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrInvalidEmail), errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooWeak):
				respondError(c, http.StatusBadRequest, "validation_error", err.Error())
			case errors.Is(err, auth.ErrEmailTaken):
				respondError(c, http.StatusConflict, "conflict", err.Error())
			default:
				log.Printf("[ERROR] registerHandler: %v", err)
				respondError(c, http.StatusInternalServerError, "internal_error", "failed to register user")
			}
			return
		}

		c.JSON(http.StatusCreated, gin.H{"message": "registered successfully"})
	}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func loginHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "validation_error", err.Error())
			return
		}

		token, err := authService.Login(c.Request.Context(), req.Email, req.Password, c.ClientIP())
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				respondError(c, http.StatusUnauthorized, "unauthorized", err.Error())
				return
			}
			log.Printf("[ERROR] loginHandler: %v", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "failed to login")
			return
		}

		claims, err := authService.ValidateToken(token)
		if err != nil {
			log.Printf("[ERROR] loginHandler: validate freshly issued token: %v", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "failed to login")
			return
		}

		u, err := authService.GetUserByID(c.Request.Context(), claims.UserID)
		if err != nil {
			log.Printf("[ERROR] loginHandler: get user by id: %v", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "failed to login")
			return
		}

		c.JSON(http.StatusOK, gin.H{"token": token, "user": toUserResponse(u)})
	}
}

func meHandler(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondError(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		u, err := authService.GetUserByID(c.Request.Context(), userID)
		if err != nil {
			if errors.Is(err, auth.ErrUserNotFound) {
				respondError(c, http.StatusNotFound, "not_found", "user not found")
				return
			}
			log.Printf("[ERROR] meHandler: %v", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "failed to load user")
			return
		}

		c.JSON(http.StatusOK, gin.H{"user": toUserResponse(u)})
	}
}

// respondUserServiceError maps a UserService error to the appropriate HTTP
// status and {"success":false,...} response, logging unexpected/internal
// errors without exposing their technical detail to the client.
func respondUserServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, user.ErrUserNotFound), errors.Is(err, user.ErrSubscriptionNotFound):
		respondFail(c, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, user.ErrInvalidAssetType),
		errors.Is(err, user.ErrInvalidAlertType),
		errors.Is(err, user.ErrMissingPriceThreshold),
		errors.Is(err, user.ErrMissingPctThreshold),
		errors.Is(err, user.ErrEmptyKeyword),
		errors.Is(err, user.ErrKeywordTooLong),
		errors.Is(err, user.ErrInvalidExpertiseLevel),
		errors.Is(err, user.ErrInvalidPreferredLang),
		errors.Is(err, user.ErrInvalidAssetSymbol),
		errors.Is(err, user.ErrInvalidPriceValue),
		errors.Is(err, user.ErrInvalidTelegramChatID):
		respondFail(c, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, user.ErrDuplicateKeyword):
		respondFail(c, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, user.ErrMaxUniqueSymbolsReached):
		respondFail(c, http.StatusUnprocessableEntity, "limit_reached", err.Error())
	default:
		log.Printf("[ERROR] user service: %v", err)
		respondFail(c, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

type userProfileResponse struct {
	ID                     uint64   `json:"id"`
	Email                  string   `json:"email"`
	TelegramAssetChatID    *int64   `json:"telegram_asset_chat_id,omitempty"`
	TelegramSentinelChatID *int64   `json:"telegram_sentinel_chat_id,omitempty"`
	AlertCooldownHours     int      `json:"alert_cooldown_hours"`
	PreferredLanguage      string   `json:"preferred_language"`
	Devices                []string `json:"devices"`
	OSList                 []string `json:"os_list"`
	ExpertiseLevel         string   `json:"expertise_level"`
}

func toUserProfileResponse(p *user.UserProfile) userProfileResponse {
	return userProfileResponse{
		ID:                     p.ID,
		Email:                  p.Email,
		TelegramAssetChatID:    p.TelegramAssetChatID,
		TelegramSentinelChatID: p.TelegramSentinelChatID,
		AlertCooldownHours:     p.AlertCooldownHours,
		PreferredLanguage:      p.PreferredLanguage,
		Devices:                p.Devices,
		OSList:                 p.OSList,
		ExpertiseLevel:         p.ExpertiseLevel,
	}
}

func getUserProfileHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		profile, err := userService.GetProfile(userID)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusOK, toUserProfileResponse(profile), "profile retrieved")
	}
}

type updateUserProfileRequestBody struct {
	TelegramAssetChatID    *int64   `json:"telegram_asset_chat_id"`
	TelegramSentinelChatID *int64   `json:"telegram_sentinel_chat_id"`
	AlertCooldownHours     *int     `json:"alert_cooldown_hours"`
	PreferredLanguage      *string  `json:"preferred_language"`
	Devices                []string `json:"devices"`
	OSList                 []string `json:"os_list"`
	ExpertiseLevel         *string  `json:"expertise_level"`
}

func updateUserProfileHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		var req updateUserProfileRequestBody
		if err := c.ShouldBindJSON(&req); err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", err.Error())
			return
		}

		err = userService.UpdateProfile(userID, user.UpdateProfileRequest{
			TelegramAssetChatID:    req.TelegramAssetChatID,
			TelegramSentinelChatID: req.TelegramSentinelChatID,
			AlertCooldownHours:     req.AlertCooldownHours,
			PreferredLanguage:      req.PreferredLanguage,
			Devices:                req.Devices,
			OSList:                 req.OSList,
			ExpertiseLevel:         req.ExpertiseLevel,
		})
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusOK, nil, "profile updated")
	}
}

type assetSubResponse struct {
	ID                 uint64    `json:"id"`
	AssetType          string    `json:"asset_type"`
	AssetSymbol        string    `json:"asset_symbol"`
	AlertType          string    `json:"alert_type"`
	PriceLowerUSD      *float64  `json:"price_lower_usd,omitempty"`
	PriceUpperUSD      *float64  `json:"price_upper_usd,omitempty"`
	PctChangeThreshold *float64  `json:"pct_change_threshold,omitempty"`
	IsActive           bool      `json:"is_active"`
	CreatedAt          time.Time `json:"created_at"`
}

func toAssetSubResponse(s user.AssetSubscription) assetSubResponse {
	return assetSubResponse{
		ID:                 s.ID,
		AssetType:          s.AssetType,
		AssetSymbol:        s.AssetSymbol,
		AlertType:          s.AlertType,
		PriceLowerUSD:      s.PriceLowerUSD,
		PriceUpperUSD:      s.PriceUpperUSD,
		PctChangeThreshold: s.PctChangeThreshold,
		IsActive:           s.IsActive,
		CreatedAt:          s.CreatedAt,
	}
}

func listAssetSubsHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		subs, err := userService.GetAssetSubscriptions(userID)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		result := make([]assetSubResponse, 0, len(subs))
		for _, s := range subs {
			result = append(result, toAssetSubResponse(s))
		}

		respondSuccess(c, http.StatusOK, result, "asset subscriptions retrieved")
	}
}

type createAssetSubRequestBody struct {
	AssetType          string   `json:"asset_type" binding:"required"`
	AssetSymbol        string   `json:"asset_symbol" binding:"required"`
	AlertType          string   `json:"alert_type" binding:"required"`
	PriceLowerUSD      *float64 `json:"price_lower_usd"`
	PriceUpperUSD      *float64 `json:"price_upper_usd"`
	PctChangeThreshold *float64 `json:"pct_change_threshold"`
}

func createAssetSubHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		var req createAssetSubRequestBody
		if err := c.ShouldBindJSON(&req); err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", err.Error())
			return
		}

		err = userService.CreateAssetSubscription(userID, user.CreateAssetSubRequest{
			AssetType:          req.AssetType,
			AssetSymbol:        req.AssetSymbol,
			AlertType:          req.AlertType,
			PriceLowerUSD:      req.PriceLowerUSD,
			PriceUpperUSD:      req.PriceUpperUSD,
			PctChangeThreshold: req.PctChangeThreshold,
		})
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusCreated, nil, "asset subscription created")
	}
}

type updateAssetSubRequestBody struct {
	PriceLowerUSD      *float64 `json:"price_lower_usd"`
	PriceUpperUSD      *float64 `json:"price_upper_usd"`
	PctChangeThreshold *float64 `json:"pct_change_threshold"`
	AlertType          *string  `json:"alert_type"`
	IsActive           *bool    `json:"is_active"`
}

func updateAssetSubHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		subID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", "invalid subscription id")
			return
		}

		var req updateAssetSubRequestBody
		if err := c.ShouldBindJSON(&req); err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", err.Error())
			return
		}

		err = userService.UpdateAssetSubscription(userID, subID, user.UpdateAssetSubRequest{
			PriceLowerUSD:      req.PriceLowerUSD,
			PriceUpperUSD:      req.PriceUpperUSD,
			PctChangeThreshold: req.PctChangeThreshold,
			AlertType:          req.AlertType,
			IsActive:           req.IsActive,
		})
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusOK, nil, "asset subscription updated")
	}
}

func deleteAssetSubHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		subID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", "invalid subscription id")
			return
		}

		if err := userService.DeleteAssetSubscription(userID, subID); err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusOK, nil, "asset subscription deleted")
	}
}

type keywordSubResponse struct {
	ID          uint64    `json:"id"`
	Keyword     string    `json:"keyword"`
	ContextNote *string   `json:"context_note,omitempty"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

func toKeywordSubResponse(s user.KeywordSubscription) keywordSubResponse {
	return keywordSubResponse{
		ID:          s.ID,
		Keyword:     s.Keyword,
		ContextNote: s.ContextNote,
		IsActive:    s.IsActive,
		CreatedAt:   s.CreatedAt,
	}
}

func listKeywordSubsHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		subs, err := userService.GetKeywordSubscriptions(userID)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		result := make([]keywordSubResponse, 0, len(subs))
		for _, s := range subs {
			result = append(result, toKeywordSubResponse(s))
		}

		respondSuccess(c, http.StatusOK, result, "keyword subscriptions retrieved")
	}
}

type createKeywordSubRequestBody struct {
	Keyword     string  `json:"keyword" binding:"required"`
	ContextNote *string `json:"context_note"`
}

func createKeywordSubHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		var req createKeywordSubRequestBody
		if err := c.ShouldBindJSON(&req); err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", err.Error())
			return
		}

		err = userService.CreateKeywordSubscription(userID, user.CreateKeywordSubRequest{
			Keyword:     req.Keyword,
			ContextNote: req.ContextNote,
		})
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusCreated, nil, "keyword subscription created")
	}
}

func deleteKeywordSubHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		subID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			respondFail(c, http.StatusBadRequest, "validation_error", "invalid subscription id")
			return
		}

		if err := userService.DeleteKeywordSubscription(userID, subID); err != nil {
			respondUserServiceError(c, err)
			return
		}

		respondSuccess(c, http.StatusOK, nil, "keyword subscription deleted")
	}
}

type notificationLogResponse struct {
	ID             uint64    `json:"id"`
	NotifType      string    `json:"notif_type"`
	AssetSymbol    *string   `json:"asset_symbol,omitempty"`
	Keyword        *string   `json:"keyword,omitempty"`
	ContentSummary string    `json:"content_summary"`
	SentAt         time.Time `json:"sent_at"`
	Status         string    `json:"status"`
}

func toNotificationLogResponse(l user.NotificationLog) notificationLogResponse {
	return notificationLogResponse{
		ID:             l.ID,
		NotifType:      l.NotifType,
		AssetSymbol:    l.AssetSymbol,
		Keyword:        l.Keyword,
		ContentSummary: l.ContentSummary,
		SentAt:         l.SentAt,
		Status:         l.Status,
	}
}

const (
	defaultNotificationLimit         = 20
	maxNotificationLimit             = 100
	dashboardRecentNotificationCount = 5
)

func parseIntQuery(c *gin.Context, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func notificationHistoryHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		limit := parseIntQuery(c, "limit", defaultNotificationLimit)
		if limit <= 0 {
			limit = defaultNotificationLimit
		}
		if limit > maxNotificationLimit {
			limit = maxNotificationLimit
		}

		offset := parseIntQuery(c, "offset", 0)
		if offset < 0 {
			offset = 0
		}

		notifType := c.Query("type")
		if notifType != "" && notifType != "asset" && notifType != "sentinel" {
			respondFail(c, http.StatusBadRequest, "validation_error", `type must be "asset", "sentinel", or omitted`)
			return
		}

		logs, total, err := userService.GetNotificationHistory(userID, limit, offset, notifType)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		result := make([]notificationLogResponse, 0, len(logs))
		for _, l := range logs {
			result = append(result, toNotificationLogResponse(l))
		}

		respondPaginated(c, http.StatusOK, result, total, limit, offset)
	}
}

type marketDataResponse struct {
	Symbol       string    `json:"symbol"`
	PriceUSD     float64   `json:"price_usd"`
	PriceIDR     float64   `json:"price_idr"`
	ChangePct24h float64   `json:"change_pct_24h"`
	LastFetched  time.Time `json:"last_fetched"`
	Source       string    `json:"source"`
}

func toMarketDataResponse(m user.MarketData) marketDataResponse {
	return marketDataResponse{
		Symbol:       m.Symbol,
		PriceUSD:     m.PriceUSD,
		PriceIDR:     m.PriceIDR,
		ChangePct24h: m.ChangePct24h,
		LastFetched:  m.LastFetched,
		Source:       m.Source,
	}
}

func marketSnapshotHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := userService.GetMarketSnapshot()
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		result := make([]marketDataResponse, 0, len(data))
		for _, m := range data {
			result = append(result, toMarketDataResponse(m))
		}

		respondSuccess(c, http.StatusOK, result, "market snapshot retrieved")
	}
}

type dashboardResponse struct {
	AssetCount          int                       `json:"asset_count"`
	KeywordCount        int                       `json:"keyword_count"`
	LastAssetAlert      *time.Time                `json:"last_asset_alert"`
	LastSentinelAlert   *time.Time                `json:"last_sentinel_alert"`
	MarketSnapshot      []marketDataResponse      `json:"market_snapshot"`
	RecentNotifications []notificationLogResponse `json:"recent_notifications"`
}

func dashboardHandler(userService *user.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.GetCurrentUserID(c)
		if err != nil {
			respondFail(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		assetSubs, err := userService.GetAssetSubscriptions(userID)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}
		activeAssetCount := 0
		for _, s := range assetSubs {
			if s.IsActive {
				activeAssetCount++
			}
		}

		keywordSubs, err := userService.GetKeywordSubscriptions(userID)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}
		activeKeywordCount := 0
		for _, s := range keywordSubs {
			if s.IsActive {
				activeKeywordCount++
			}
		}

		lastAsset, lastSentinel, err := userService.GetLastAlertTimestamps(userID)
		if err != nil {
			respondUserServiceError(c, err)
			return
		}

		snapshot, err := userService.GetMarketSnapshot()
		if err != nil {
			respondUserServiceError(c, err)
			return
		}
		snapshotResult := make([]marketDataResponse, 0, len(snapshot))
		for _, m := range snapshot {
			snapshotResult = append(snapshotResult, toMarketDataResponse(m))
		}

		recentLogs, _, err := userService.GetNotificationHistory(userID, dashboardRecentNotificationCount, 0, "")
		if err != nil {
			respondUserServiceError(c, err)
			return
		}
		recentResult := make([]notificationLogResponse, 0, len(recentLogs))
		for _, l := range recentLogs {
			recentResult = append(recentResult, toNotificationLogResponse(l))
		}

		respondSuccess(c, http.StatusOK, dashboardResponse{
			AssetCount:          activeAssetCount,
			KeywordCount:        activeKeywordCount,
			LastAssetAlert:      lastAsset,
			LastSentinelAlert:   lastSentinel,
			MarketSnapshot:      snapshotResult,
			RecentNotifications: recentResult,
		}, "dashboard data retrieved")
	}
}

type marketQuoteResponse struct {
	Symbol       string    `json:"symbol"`
	PriceUSD     float64   `json:"price_usd"`
	PriceIDR     float64   `json:"price_idr"`
	ChangePct24h float64   `json:"change_pct_24h"`
	LastFetched  time.Time `json:"last_fetched"`
	Source       string    `json:"source"`
}

func marketQuoteHandler(sqlDB *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		symbol := c.Param("symbol")

		var q marketQuoteResponse
		err := sqlDB.QueryRowContext(c.Request.Context(), `
			SELECT symbol, price_usd, price_idr, change_pct_24h, last_fetched, source
			FROM market_cache
			WHERE symbol = ?`, symbol,
		).Scan(&q.Symbol, &q.PriceUSD, &q.PriceIDR, &q.ChangePct24h, &q.LastFetched, &q.Source)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				respondError(c, http.StatusNotFound, "not_found", "symbol not found in cache")
				return
			}
			log.Printf("[ERROR] marketQuoteHandler: %v", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "failed to load market data")
			return
		}

		respondData(c, http.StatusOK, q, "market data retrieved")
	}
}
