// Package auth owns user registration, login, and JWT issuance/validation
// for the WatchTower API, backed directly by the users/user_profiles
// tables.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	bcryptCost              = 12
	minPasswordLength       = 8
	mysqlDuplicateEntryCode = 1062
	assetBotType            = "asset"
	sentinelBotType         = "sentinel"
	tokenIssuer             = "watchtower"

	// authCookieName/authCookiePath identify the httpOnly cookie Login sets
	// and Logout clears. The JWT itself is never exposed to frontend
	// JavaScript this way — only sent automatically by the browser on
	// same-site requests (see AuthMiddleware in middleware.go, which reads
	// it back via c.Cookie), which is what closes off the XSS-token-theft
	// risk a localStorage-held token has.
	authCookieName = "watchtower_token"
	authCookiePath = "/"

	// failedLoginWindow/failedLoginThreshold configure the brute-force
	// warning: 5+ failed logins from one IP within 15 minutes gets logged.
	failedLoginWindow    = 15 * time.Minute
	failedLoginThreshold = 5

	// registerDuplicateJitterMinMs/MaxMs pad the response time of a
	// duplicate-email registration attempt, as modest defense-in-depth
	// against user-enumeration timing analysis on this endpoint.
	registerDuplicateJitterMinMs = 50
	registerDuplicateJitterMaxMs = 150
)

// emailPattern is a stricter email format check than net/mail's RFC-5322
// parser (which accepts forms like "Name <addr@example.com>" that
// shouldn't be valid registration input here).
var emailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// Password complexity requirements beyond minimum length.
var (
	passwordHasUpper   = regexp.MustCompile(`[A-Z]`)
	passwordHasLower   = regexp.MustCompile(`[a-z]`)
	passwordHasDigit   = regexp.MustCompile(`[0-9]`)
	passwordHasSpecial = regexp.MustCompile(`[!@#$%^&*]`)
)

// Errors returned by the auth Service. Handlers can compare against these
// with errors.Is to decide which HTTP status/code to respond with.
var (
	ErrInvalidEmail       = errors.New("invalid email format")
	ErrPasswordTooShort   = errors.New("password must be at least 8 characters")
	ErrPasswordTooWeak    = errors.New("password must contain at least one uppercase letter, one lowercase letter, one number, and one special character (!@#$%^&*)")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserNotFound       = errors.New("user not found")
	ErrInvalidBotType     = errors.New(`bot type must be "asset" or "sentinel"`)
	ErrInvalidToken       = errors.New("invalid or expired token")
)

// dummyBcryptHash is compared against on a login attempt for an email that
// doesn't exist, so that path takes roughly as long as the "wrong password"
// path below (which always runs a real bcrypt comparison) — preventing an
// attacker from telling "no such account" apart from "wrong password"
// purely by measuring response latency (bcrypt is deliberately slow, so
// skipping it entirely on the not-found path would otherwise be a
// measurable, exploitable timing side-channel).
var dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing-safety"), bcryptCost)

// failedLoginTracker counts failed login attempts per source IP over a
// rolling window, purely for logging a brute-force warning — it never
// blocks a login itself (that's the job of the rate limiter middleware).
type failedLoginTracker struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

var loginAttempts = &failedLoginTracker{attempts: make(map[string][]time.Time)}

func (t *failedLoginTracker) recordFailure(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-failedLoginWindow)

	recent := make([]time.Time, 0, len(t.attempts[ip])+1)
	for _, at := range t.attempts[ip] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	recent = append(recent, now)
	t.attempts[ip] = recent

	if len(recent) >= failedLoginThreshold {
		log.Printf("[WARN] Login: %d failed login attempts from %s in the last %s", len(recent), ip, failedLoginWindow)
	}
}

// randomJitter returns a random duration in [minMs, maxMs), using
// crypto/rand so the delay itself isn't a predictable/gameable value.
func randomJitter(minMs, maxMs int) time.Duration {
	span := int64(maxMs - minMs)
	if span <= 0 {
		return time.Duration(minMs) * time.Millisecond
	}
	n, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return time.Duration(minMs) * time.Millisecond
	}
	return time.Duration(minMs+int(n.Int64())) * time.Millisecond
}

// User mirrors a row in the users table.
type User struct {
	ID                     uint64
	Email                  string
	PasswordHash           string
	TelegramAssetChatID    *int64
	TelegramSentinelChatID *int64
	AlertCooldownHours     int
	PreferredLanguage      string
	IsActive               bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Claims are the JWT claims WatchTower issues for authenticated users.
type Claims struct {
	UserID            uint64 `json:"user_id"`
	Email             string `json:"email"`
	PreferredLanguage string `json:"preferred_language"`
	jwt.RegisteredClaims
}

// Service provides registration, login, and token validation for
// WatchTower accounts.
type Service struct {
	db        *sql.DB
	jwtSecret string
	jwtExpiry time.Duration
}

// NewService builds an auth Service backed by db, signing tokens with
// jwtSecret and expiring them after jwtExpiryHours.
func NewService(db *sql.DB, jwtSecret string, jwtExpiryHours int) *Service {
	return &Service{
		db:        db,
		jwtSecret: jwtSecret,
		jwtExpiry: time.Duration(jwtExpiryHours) * time.Hour,
	}
}

// Register validates email/password, creates a new user with a
// bcrypt-hashed password, and creates the matching (empty) user_profiles
// row. The email is lower-cased before being stored.
func (s *Service) Register(ctx context.Context, email, password string) error {
	email = normalizeEmail(email)

	if !emailPattern.MatchString(email) {
		return ErrInvalidEmail
	}
	if len(password) < minPasswordLength {
		return ErrPasswordTooShort
	}
	if !passwordHasUpper.MatchString(password) || !passwordHasLower.MatchString(password) ||
		!passwordHasDigit.MatchString(password) || !passwordHasSpecial.MatchString(password) {
		return ErrPasswordTooWeak
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		log.Printf("[ERROR] Register: hash password: %v", err)
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[ERROR] Register: begin transaction: %v", err)
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (email, password_hash) VALUES (?, ?)`,
		email, string(hash),
	)
	if err != nil {
		if isDuplicateEntry(err) {
			// Modest timing defense-in-depth: see registerDuplicateJitterMinMs.
			time.Sleep(randomJitter(registerDuplicateJitterMinMs, registerDuplicateJitterMaxMs))
			return ErrEmailTaken
		}
		log.Printf("[ERROR] Register: insert user: %v", err)
		return fmt.Errorf("insert user: %w", err)
	}

	userID, err := res.LastInsertId()
	if err != nil {
		log.Printf("[ERROR] Register: last insert id: %v", err)
		return fmt.Errorf("last insert id: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_profiles (user_id) VALUES (?)`, userID,
	); err != nil {
		log.Printf("[ERROR] Register: create user profile: %v", err)
		return fmt.Errorf("create user profile: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[ERROR] Register: commit transaction: %v", err)
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// Login verifies an email/password pair and, on success, sets a signed JWT
// as an httpOnly cookie on c's response (see setAuthCookie) instead of
// returning the token to the caller — the browser then attaches it
// automatically on subsequent requests, and frontend JavaScript never has
// direct access to it, closing off the token-theft-via-XSS risk a
// localStorage-held token has. isProduction controls the cookie's Secure
// and SameSite attributes (see setAuthCookie); the client's IP is read from
// c for security logging (failed-attempt tracking) — never persisted.
func (s *Service) Login(c *gin.Context, email, password string, isProduction bool) (*User, error) {
	ctx := c.Request.Context()
	email = normalizeEmail(email)

	u, err := s.getUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Run a real bcrypt comparison against a dummy hash so this
			// path takes about as long as the "wrong password" path below
			// — otherwise an attacker could tell "no such account" apart
			// from "wrong password" purely by response timing, since a
			// non-existent email currently returns almost instantly while
			// bcrypt itself is deliberately slow.
			_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
			s.logFailedLogin(c.ClientIP(), email)
			return nil, ErrInvalidCredentials
		}
		log.Printf("[ERROR] Login: get user by email: %v", err)
		return nil, fmt.Errorf("get user by email: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		s.logFailedLogin(c.ClientIP(), email)
		return nil, ErrInvalidCredentials
	}

	token, err := s.generateToken(u)
	if err != nil {
		log.Printf("[ERROR] Login: generate token: %v", err)
		return nil, fmt.Errorf("generate token: %w", err)
	}

	s.setAuthCookie(c, token, isProduction)

	return u, nil
}

// setAuthCookie sets the httpOnly JWT cookie Login issues. Secure and
// SameSite both derive from isProduction rather than being independently
// configurable: in development (plain HTTP) Secure must be false or
// browsers refuse to store the cookie at all, and SameSite=Lax is
// appropriate for that same-origin-ish local setup; in production (HTTPS)
// Secure=true is required and SameSite=Strict is safe to tighten to, since
// there's no legitimate cross-site flow that needs the cookie sent. Tying
// both to the single isProduction flag (rather than adding separate
// COOKIE_SECURE/COOKIE_SAMESITE env vars) avoids a configuration that could
// contradict APP_ENV and accidentally ship a non-Secure cookie to
// production.
func (s *Service) setAuthCookie(c *gin.Context, token string, isProduction bool) {
	sameSite := http.SameSiteLaxMode
	if isProduction {
		sameSite = http.SameSiteStrictMode
	}
	c.SetSameSite(sameSite)
	c.SetCookie(authCookieName, token, int(s.jwtExpiry.Seconds()), authCookiePath, "", isProduction, true)
}

// Logout clears the httpOnly auth cookie Login sets. Browsers match
// cookies for deletion by name/domain/path only (not by Secure/HttpOnly/
// SameSite), so a fixed maxAge=-1 delete works regardless of which
// isProduction value the cookie was originally set with.
func (s *Service) Logout(c *gin.Context) {
	c.SetCookie(authCookieName, "", -1, authCookiePath, "", false, true)
}

// logFailedLogin records a failed login attempt for brute-force detection.
// It never logs the attempted password.
func (s *Service) logFailedLogin(ip, email string) {
	log.Printf("[WARN] Login: failed login attempt for %s from %s", email, ip)
	loginAttempts.recordFailure(ip)
}

func (s *Service) generateToken(u *User) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:            u.ID,
		Email:             u.Email,
		PreferredLanguage: u.PreferredLanguage,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.jwtExpiry)),
			Subject:   fmt.Sprintf("%d", u.ID),
			Issuer:    tokenIssuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// jwtParser rejects "alg: none" and any non-HS256 algorithm outright via
// WithValidMethods (checked by the library before the key function below is
// even called, rather than via a manual type assertion on the token's
// signing method), requires a valid, unexpired exp claim, requires an iat
// claim, and — since every token this service issues sets one — requires
// the iss claim to match tokenIssuer.
var jwtParser = jwt.NewParser(
	jwt.WithValidMethods([]string{"HS256"}),
	jwt.WithExpirationRequired(),
	jwt.WithIssuedAt(),
	jwt.WithIssuer(tokenIssuer),
)

// ValidateToken parses and verifies tokenString, returning its claims if
// valid.
func (s *Service) ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwtParser.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(s.jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// GetUserByID fetches a single user by ID.
func (s *Service) GetUserByID(ctx context.Context, id uint64) (*User, error) {
	row := s.db.QueryRowContext(ctx, selectUserColumns+` WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Service) getUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(ctx, selectUserColumns+` WHERE email = ?`, email)
	return scanUser(row)
}

// UpdateTelegramChatID sets telegram_asset_chat_id or
// telegram_sentinel_chat_id for userID depending on botType ("asset" or
// "sentinel").
func (s *Service) UpdateTelegramChatID(ctx context.Context, userID uint64, botType string, chatID int64) error {
	var column string
	switch botType {
	case assetBotType:
		column = "telegram_asset_chat_id"
	case sentinelBotType:
		column = "telegram_sentinel_chat_id"
	default:
		return ErrInvalidBotType
	}

	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE users SET %s = ? WHERE id = ?`, column),
		chatID, userID,
	)
	if err != nil {
		log.Printf("[ERROR] UpdateTelegramChatID: update user %d: %v", userID, err)
		return fmt.Errorf("update telegram chat id: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		log.Printf("[ERROR] UpdateTelegramChatID: rows affected: %v", err)
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return ErrUserNotFound
	}

	return nil
}

const selectUserColumns = `
	SELECT id, email, password_hash, telegram_asset_chat_id, telegram_sentinel_chat_id,
	       alert_cooldown_hours, preferred_language, is_active, created_at, updated_at
	FROM users`

func scanUser(row *sql.Row) (*User, error) {
	var (
		u                      User
		telegramAssetChatID    sql.NullInt64
		telegramSentinelChatID sql.NullInt64
	)

	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &telegramAssetChatID, &telegramSentinelChatID,
		&u.AlertCooldownHours, &u.PreferredLanguage, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}

	if telegramAssetChatID.Valid {
		u.TelegramAssetChatID = &telegramAssetChatID.Int64
	}
	if telegramSentinelChatID.Valid {
		u.TelegramSentinelChatID = &telegramSentinelChatID.Int64
	}

	return &u, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isDuplicateEntry(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == mysqlDuplicateEntryCode
	}
	return false
}

// HashPassword hashes a plaintext password using bcrypt. Kept as a
// standalone helper for callers (e.g. internal/user) that manage their own
// persistence outside of Service.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(hash), nil
}

// ComparePassword reports whether password matches the given bcrypt hash.
func ComparePassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
