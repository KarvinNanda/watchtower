// Package user provides persistence and authentication for WatchTower
// accounts backed by the users table.
package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/karvin-nanda/watchtower/internal/auth"
)

// ErrNotFound is returned when no user matches the given lookup.
var ErrNotFound = errors.New("user: not found")

// ErrEmailTaken is returned when registering with an email already in use.
var ErrEmailTaken = errors.New("user: email already registered")

// ErrInvalidCredentials is returned when login email/password do not match.
var ErrInvalidCredentials = errors.New("user: invalid email or password")

const mysqlDuplicateEntryCode = 1062

// User mirrors a row in the users table.
type User struct {
	ID                     uint64
	Email                  string
	PasswordHash           string
	TelegramAssetChatID    sql.NullInt64
	TelegramSentinelChatID sql.NullInt64
	AlertCooldownHours     int
	PreferredLanguage      string
	IsActive               bool
}

// Repository provides CRUD access to the users table.
type Repository struct {
	db *sql.DB
}

// NewRepository builds a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const selectUserColumns = `
	SELECT id, email, password_hash, telegram_asset_chat_id, telegram_sentinel_chat_id,
	       alert_cooldown_hours, preferred_language, is_active
	FROM users`

// Register creates a new user with a bcrypt-hashed password.
func (r *Repository) Register(ctx context.Context, email, password string) (*User, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}

	res, err := r.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash) VALUES (?, ?)`,
		email, hash,
	)
	if err != nil {
		if isDuplicateEntry(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("user: insert: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("user: last insert id: %w", err)
	}
	if id < 0 {
		return nil, fmt.Errorf("user: last insert id: negative id %d", id)
	}

	return r.GetByID(ctx, uint64(id))
}

// Authenticate verifies an email/password pair and returns the matching user.
func (r *Repository) Authenticate(ctx context.Context, email, password string) (*User, error) {
	u, err := r.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !auth.ComparePassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}

	return u, nil
}

// GetByID fetches a single user by ID.
func (r *Repository) GetByID(ctx context.Context, id uint64) (*User, error) {
	row := r.db.QueryRowContext(ctx, selectUserColumns+` WHERE id = ?`, id)
	return scanUser(row)
}

// GetByEmail fetches a single user by email.
func (r *Repository) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := r.db.QueryRowContext(ctx, selectUserColumns+` WHERE email = ?`, email)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.TelegramAssetChatID, &u.TelegramSentinelChatID,
		&u.AlertCooldownHours, &u.PreferredLanguage, &u.IsActive,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("user: scan: %w", err)
	}
	return &u, nil
}

func isDuplicateEntry(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == mysqlDuplicateEntryCode
	}
	return false
}

// dbTimeout bounds every UserService query, matching the timeout
// convention used throughout the rest of the codebase (scheduler, cache).
const dbTimeout = 30 * time.Second

const maxKeywordLength = 100

// Errors returned by UserService. Handlers can compare against these with
// errors.Is to decide which HTTP status/code to respond with.
var (
	ErrUserNotFound            = errors.New("user: not found")
	ErrInvalidAssetType        = errors.New("asset_type must be one of: stock, crypto, gold")
	ErrInvalidAlertType        = errors.New("alert_type must be one of: price_threshold, pct_change, both")
	ErrMissingPriceThreshold   = errors.New("price_lower_usd or price_upper_usd is required for this alert_type")
	ErrMissingPctThreshold     = errors.New("pct_change_threshold is required for this alert_type")
	ErrMaxUniqueSymbolsReached = errors.New("maximum number of unique tracked symbols reached")
	ErrSubscriptionNotFound    = errors.New("subscription not found")
	ErrEmptyKeyword            = errors.New("keyword cannot be empty")
	ErrKeywordTooLong          = errors.New("keyword must be at most 100 characters")
	ErrDuplicateKeyword        = errors.New("keyword already subscribed")
	ErrInvalidExpertiseLevel   = errors.New("expertise_level must be one of: beginner, intermediate, advanced")
	ErrInvalidPreferredLang    = errors.New("preferred_language must be one of: id, en")
	ErrInvalidAssetSymbol      = errors.New("asset_symbol must be 1-20 uppercase letters, digits, or hyphens")
	ErrInvalidPriceValue       = errors.New("price and threshold values must be positive and at most 999999999")
	ErrInvalidTelegramChatID   = errors.New("telegram chat id must be a positive number")
)

var validAssetTypes = map[string]bool{"stock": true, "crypto": true, "gold": true}
var validAlertTypes = map[string]bool{"price_threshold": true, "pct_change": true, "both": true}
var validExpertiseLevels = map[string]bool{"beginner": true, "intermediate": true, "advanced": true}
var validPreferredLangs = map[string]bool{"id": true, "en": true}

// UserService handles user profile, subscription, notification history, and
// market snapshot operations for the dashboard API.
type UserService struct {
	db               *sql.DB
	maxUniqueSymbols int
}

// NewUserService builds a UserService backed by db. maxUniqueSymbols caps
// how many distinct asset symbols the whole system may track at once
// (MAX_UNIQUE_SYMBOLS) — enforced in CreateAssetSubscription.
func NewUserService(db *sql.DB, maxUniqueSymbols int) *UserService {
	return &UserService{db: db, maxUniqueSymbols: maxUniqueSymbols}
}

// UserProfile is the full profile view combining the users and
// user_profiles tables.
type UserProfile struct {
	ID                     uint64
	Email                  string
	TelegramAssetChatID    *int64
	TelegramSentinelChatID *int64
	AlertCooldownHours     int
	PreferredLanguage      string
	Devices                []string
	OSList                 []string
	ExpertiseLevel         string
}

// GetProfile returns userID's full profile, joining users and
// user_profiles.
func (s *UserService) GetProfile(userID uint64) (*UserProfile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	var (
		p                      UserProfile
		telegramAssetChatID    sql.NullInt64
		telegramSentinelChatID sql.NullInt64
		devicesRaw             sql.NullString
		osListRaw              sql.NullString
		expertiseLevel         sql.NullString
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT
			u.id, u.email, u.telegram_asset_chat_id, u.telegram_sentinel_chat_id,
			u.alert_cooldown_hours, u.preferred_language,
			up.devices, up.os_list, up.expertise_level
		FROM users u
		LEFT JOIN user_profiles up ON up.user_id = u.id
		WHERE u.id = ?`, userID,
	).Scan(
		&p.ID, &p.Email, &telegramAssetChatID, &telegramSentinelChatID,
		&p.AlertCooldownHours, &p.PreferredLanguage,
		&devicesRaw, &osListRaw, &expertiseLevel,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("user: query profile: %w", err)
	}

	if telegramAssetChatID.Valid {
		p.TelegramAssetChatID = &telegramAssetChatID.Int64
	}
	if telegramSentinelChatID.Valid {
		p.TelegramSentinelChatID = &telegramSentinelChatID.Int64
	}

	p.Devices = []string{}
	if devicesRaw.Valid && devicesRaw.String != "" {
		if err := json.Unmarshal([]byte(devicesRaw.String), &p.Devices); err != nil {
			return nil, fmt.Errorf("user: parse devices: %w", err)
		}
	}

	p.OSList = []string{}
	if osListRaw.Valid && osListRaw.String != "" {
		if err := json.Unmarshal([]byte(osListRaw.String), &p.OSList); err != nil {
			return nil, fmt.Errorf("user: parse os_list: %w", err)
		}
	}

	p.ExpertiseLevel = "beginner"
	if expertiseLevel.Valid {
		p.ExpertiseLevel = expertiseLevel.String
	}

	return &p, nil
}

// UpdateProfileRequest carries partial profile updates — every field is
// nullable, and only the fields actually provided (non-nil, or non-nil
// slices for Devices/OSList) are updated.
type UpdateProfileRequest struct {
	TelegramAssetChatID    *int64
	TelegramSentinelChatID *int64
	AlertCooldownHours     *int
	PreferredLanguage      *string
	Devices                []string
	OSList                 []string
	ExpertiseLevel         *string
}

// UpdateProfile applies a partial update to userID's users/user_profiles
// rows. Only fields actually provided in req are changed.
func (s *UserService) UpdateProfile(userID uint64, req UpdateProfileRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	if req.ExpertiseLevel != nil && !validExpertiseLevels[*req.ExpertiseLevel] {
		return ErrInvalidExpertiseLevel
	}
	if req.PreferredLanguage != nil && !validPreferredLangs[*req.PreferredLanguage] {
		return ErrInvalidPreferredLang
	}
	if req.TelegramAssetChatID != nil && *req.TelegramAssetChatID <= 0 {
		return ErrInvalidTelegramChatID
	}
	if req.TelegramSentinelChatID != nil && *req.TelegramSentinelChatID <= 0 {
		return ErrInvalidTelegramChatID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user: begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := updateUsersTable(ctx, tx, userID, req); err != nil {
		return err
	}
	if err := updateUserProfileTable(ctx, tx, userID, req); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("user: commit transaction: %w", err)
	}

	return nil
}

func updateUsersTable(ctx context.Context, tx *sql.Tx, userID uint64, req UpdateProfileRequest) error {
	var setClauses []string
	var args []interface{}

	if req.TelegramAssetChatID != nil {
		setClauses = append(setClauses, "telegram_asset_chat_id = ?")
		args = append(args, *req.TelegramAssetChatID)
	}
	if req.TelegramSentinelChatID != nil {
		setClauses = append(setClauses, "telegram_sentinel_chat_id = ?")
		args = append(args, *req.TelegramSentinelChatID)
	}
	if req.AlertCooldownHours != nil {
		setClauses = append(setClauses, "alert_cooldown_hours = ?")
		args = append(args, *req.AlertCooldownHours)
	}
	if req.PreferredLanguage != nil {
		setClauses = append(setClauses, "preferred_language = ?")
		args = append(args, *req.PreferredLanguage)
	}

	if len(setClauses) == 0 {
		return nil
	}

	args = append(args, userID)
	// #nosec G201 -- setClauses only ever contains hardcoded "col = ?"
	// literals chosen above; all user-supplied values flow through args
	// as bind parameters, never interpolated into the query string.
	query := fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("user: update users: %w", err)
	}
	return nil
}

func updateUserProfileTable(ctx context.Context, tx *sql.Tx, userID uint64, req UpdateProfileRequest) error {
	var setClauses []string
	var args []interface{}

	if req.Devices != nil {
		devicesJSON, err := json.Marshal(req.Devices)
		if err != nil {
			return fmt.Errorf("user: marshal devices: %w", err)
		}
		setClauses = append(setClauses, "devices = ?")
		args = append(args, string(devicesJSON))
	}
	if req.OSList != nil {
		osJSON, err := json.Marshal(req.OSList)
		if err != nil {
			return fmt.Errorf("user: marshal os_list: %w", err)
		}
		setClauses = append(setClauses, "os_list = ?")
		args = append(args, string(osJSON))
	}
	if req.ExpertiseLevel != nil {
		setClauses = append(setClauses, "expertise_level = ?")
		args = append(args, *req.ExpertiseLevel)
	}

	if len(setClauses) == 0 {
		return nil
	}

	// Every user already has a user_profiles row created at registration
	// (see auth.Service.Register), so a plain UPDATE is sufficient.
	args = append(args, userID)
	// #nosec G201 -- setClauses only ever contains hardcoded "col = ?"
	// literals chosen above; all user-supplied values flow through args
	// as bind parameters, never interpolated into the query string.
	query := fmt.Sprintf("UPDATE user_profiles SET %s WHERE user_id = ?", strings.Join(setClauses, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("user: update user_profiles: %w", err)
	}
	return nil
}

// AssetSubscription mirrors a row in the asset_subscriptions table.
type AssetSubscription struct {
	ID                 uint64
	AssetType          string
	AssetSymbol        string
	AlertType          string
	PriceLowerUSD      *float64
	PriceUpperUSD      *float64
	PctChangeThreshold *float64
	IsActive           bool
	CreatedAt          time.Time
}

// GetAssetSubscriptions returns every asset subscription (active or not)
// owned by userID, most recent first.
func (s *UserService) GetAssetSubscriptions(userID uint64) ([]AssetSubscription, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, asset_type, asset_symbol, alert_type, price_lower_usd, price_upper_usd, pct_change_threshold, is_active, created_at
		FROM asset_subscriptions
		WHERE user_id = ?
		ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("user: query asset_subscriptions: %w", err)
	}
	defer rows.Close()

	subs := make([]AssetSubscription, 0)
	for rows.Next() {
		var (
			sub    AssetSubscription
			lower  sql.NullFloat64
			upper  sql.NullFloat64
			pctChg sql.NullFloat64
		)
		if err := rows.Scan(&sub.ID, &sub.AssetType, &sub.AssetSymbol, &sub.AlertType,
			&lower, &upper, &pctChg, &sub.IsActive, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("user: scan asset_subscriptions: %w", err)
		}
		if lower.Valid {
			sub.PriceLowerUSD = &lower.Float64
		}
		if upper.Valid {
			sub.PriceUpperUSD = &upper.Float64
		}
		if pctChg.Valid {
			sub.PctChangeThreshold = &pctChg.Float64
		}
		subs = append(subs, sub)
	}

	return subs, rows.Err()
}

// CreateAssetSubRequest is the input to CreateAssetSubscription.
type CreateAssetSubRequest struct {
	AssetType          string
	AssetSymbol        string
	AlertType          string
	PriceLowerUSD      *float64
	PriceUpperUSD      *float64
	PctChangeThreshold *float64
}

// assetSymbolPattern restricts a sanitized asset symbol to uppercase
// letters, digits, and hyphens (e.g. "BTC", "BRK-B").
var assetSymbolPattern = regexp.MustCompile(`^[A-Z0-9-]+$`)

const (
	maxAssetSymbolLength = 20
	maxPriceValue        = 999999999
)

// sanitizeAssetSymbol uppercases, trims, and truncates a raw asset symbol
// before it's validated against assetSymbolPattern.
func sanitizeAssetSymbol(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if len(symbol) > maxAssetSymbolLength {
		symbol = symbol[:maxAssetSymbolLength]
	}
	return symbol
}

// isValidPrice reports whether v is a sane, positive price/threshold value.
func isValidPrice(v float64) bool {
	return v > 0 && v <= maxPriceValue
}

// keywordDisallowedChars matches anything other than a lowercase letter,
// digit, space, hyphen, underscore, or dot.
var keywordDisallowedChars = regexp.MustCompile(`[^a-z0-9 _.\-]`)

// sanitizeKeyword trims, lowercases, and strips any character outside the
// allowed set from a raw keyword before it's validated/stored.
func sanitizeKeyword(keyword string) string {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	return keywordDisallowedChars.ReplaceAllString(keyword, "")
}

// CreateAssetSubscription validates and inserts a new asset subscription
// for userID.
func (s *UserService) CreateAssetSubscription(userID uint64, req CreateAssetSubRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	if !validAssetTypes[req.AssetType] {
		return ErrInvalidAssetType
	}
	if !validAlertTypes[req.AlertType] {
		return ErrInvalidAlertType
	}

	req.AssetSymbol = sanitizeAssetSymbol(req.AssetSymbol)
	if req.AssetSymbol == "" || !assetSymbolPattern.MatchString(req.AssetSymbol) {
		return ErrInvalidAssetSymbol
	}

	if req.PriceLowerUSD != nil && !isValidPrice(*req.PriceLowerUSD) {
		return ErrInvalidPriceValue
	}
	if req.PriceUpperUSD != nil && !isValidPrice(*req.PriceUpperUSD) {
		return ErrInvalidPriceValue
	}
	if req.PctChangeThreshold != nil && !isValidPrice(*req.PctChangeThreshold) {
		return ErrInvalidPriceValue
	}

	needsPriceThreshold := req.AlertType == "price_threshold" || req.AlertType == "both"
	if needsPriceThreshold && req.PriceLowerUSD == nil && req.PriceUpperUSD == nil {
		return ErrMissingPriceThreshold
	}

	needsPctThreshold := req.AlertType == "pct_change" || req.AlertType == "both"
	if needsPctThreshold && req.PctChangeThreshold == nil {
		return ErrMissingPctThreshold
	}

	if err := s.checkUniqueSymbolLimit(ctx, req.AssetSymbol); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO asset_subscriptions
			(user_id, asset_type, asset_symbol, alert_type, price_lower_usd, price_upper_usd, pct_change_threshold)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, req.AssetType, req.AssetSymbol, req.AlertType,
		req.PriceLowerUSD, req.PriceUpperUSD, req.PctChangeThreshold,
	)
	if err != nil {
		return fmt.Errorf("user: insert asset_subscriptions: %w", err)
	}

	return nil
}

// checkUniqueSymbolLimit enforces MAX_UNIQUE_SYMBOLS system-wide. A symbol
// already tracked by any active subscription never counts against the
// limit again — only genuinely new symbols do.
func (s *UserService) checkUniqueSymbolLimit(ctx context.Context, assetSymbol string) error {
	var alreadyTracked bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM asset_subscriptions WHERE asset_symbol = ? AND is_active = true
		)`, assetSymbol,
	).Scan(&alreadyTracked)
	if err != nil {
		return fmt.Errorf("user: check existing symbol: %w", err)
	}
	if alreadyTracked {
		return nil
	}

	var distinctCount int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT asset_symbol) FROM asset_subscriptions WHERE is_active = true`,
	).Scan(&distinctCount); err != nil {
		return fmt.Errorf("user: count unique symbols: %w", err)
	}

	if distinctCount >= s.maxUniqueSymbols {
		return ErrMaxUniqueSymbolsReached
	}

	return nil
}

// UpdateAssetSubRequest carries partial updates to an existing asset
// subscription — only non-nil fields are changed.
type UpdateAssetSubRequest struct {
	PriceLowerUSD      *float64
	PriceUpperUSD      *float64
	PctChangeThreshold *float64
	AlertType          *string
	IsActive           *bool
}

// UpdateAssetSubscription applies a partial update to subID, which must be
// owned by userID.
func (s *UserService) UpdateAssetSubscription(userID, subID uint64, req UpdateAssetSubRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	if req.AlertType != nil && !validAlertTypes[*req.AlertType] {
		return ErrInvalidAlertType
	}
	if req.PriceLowerUSD != nil && !isValidPrice(*req.PriceLowerUSD) {
		return ErrInvalidPriceValue
	}
	if req.PriceUpperUSD != nil && !isValidPrice(*req.PriceUpperUSD) {
		return ErrInvalidPriceValue
	}
	if req.PctChangeThreshold != nil && !isValidPrice(*req.PctChangeThreshold) {
		return ErrInvalidPriceValue
	}

	if err := s.checkAssetSubscriptionOwnership(ctx, userID, subID); err != nil {
		return err
	}

	var setClauses []string
	var args []interface{}

	if req.PriceLowerUSD != nil {
		setClauses = append(setClauses, "price_lower_usd = ?")
		args = append(args, *req.PriceLowerUSD)
	}
	if req.PriceUpperUSD != nil {
		setClauses = append(setClauses, "price_upper_usd = ?")
		args = append(args, *req.PriceUpperUSD)
	}
	if req.PctChangeThreshold != nil {
		setClauses = append(setClauses, "pct_change_threshold = ?")
		args = append(args, *req.PctChangeThreshold)
	}
	if req.AlertType != nil {
		setClauses = append(setClauses, "alert_type = ?")
		args = append(args, *req.AlertType)
	}
	if req.IsActive != nil {
		setClauses = append(setClauses, "is_active = ?")
		args = append(args, *req.IsActive)
	}

	if len(setClauses) == 0 {
		return nil
	}

	args = append(args, subID)
	// #nosec G201 -- setClauses only ever contains hardcoded "col = ?"
	// literals chosen above; all user-supplied values flow through args
	// as bind parameters, never interpolated into the query string.
	query := fmt.Sprintf("UPDATE asset_subscriptions SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("user: update asset_subscriptions: %w", err)
	}

	return nil
}

// checkAssetSubscriptionOwnership confirms subID exists and belongs to
// userID, returning ErrSubscriptionNotFound either way it doesn't (never
// distinguishing "doesn't exist" from "belongs to someone else", to avoid
// leaking the existence of other users' subscriptions).
func (s *UserService) checkAssetSubscriptionOwnership(ctx context.Context, userID, subID uint64) error {
	var ownerID uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM asset_subscriptions WHERE id = ?`, subID,
	).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSubscriptionNotFound
		}
		return fmt.Errorf("user: check asset subscription ownership: %w", err)
	}
	if ownerID != userID {
		return ErrSubscriptionNotFound
	}
	return nil
}

// DeleteAssetSubscription soft-deletes subID (is_active = false), which
// must be owned by userID.
func (s *UserService) DeleteAssetSubscription(userID, subID uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	if err := s.checkAssetSubscriptionOwnership(ctx, userID, subID); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE asset_subscriptions SET is_active = false WHERE id = ?`, subID,
	); err != nil {
		return fmt.Errorf("user: soft delete asset_subscriptions: %w", err)
	}

	return nil
}

// KeywordSubscription mirrors a row in the keyword_subscriptions table.
type KeywordSubscription struct {
	ID          uint64
	Keyword     string
	ContextNote *string
	IsActive    bool
	CreatedAt   time.Time
}

// GetKeywordSubscriptions returns every keyword subscription (active or
// not) owned by userID, most recent first.
func (s *UserService) GetKeywordSubscriptions(userID uint64) ([]KeywordSubscription, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, keyword, context_note, is_active, created_at
		FROM keyword_subscriptions
		WHERE user_id = ?
		ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("user: query keyword_subscriptions: %w", err)
	}
	defer rows.Close()

	subs := make([]KeywordSubscription, 0)
	for rows.Next() {
		var (
			sub  KeywordSubscription
			note sql.NullString
		)
		if err := rows.Scan(&sub.ID, &sub.Keyword, &note, &sub.IsActive, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("user: scan keyword_subscriptions: %w", err)
		}
		if note.Valid {
			sub.ContextNote = &note.String
		}
		subs = append(subs, sub)
	}

	return subs, rows.Err()
}

// CreateKeywordSubRequest is the input to CreateKeywordSubscription.
type CreateKeywordSubRequest struct {
	Keyword     string
	ContextNote *string
}

// CreateKeywordSubscription validates and inserts a new keyword
// subscription for userID.
func (s *UserService) CreateKeywordSubscription(userID uint64, req CreateKeywordSubRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	keyword := sanitizeKeyword(req.Keyword)
	if keyword == "" {
		return ErrEmptyKeyword
	}
	if len(keyword) > maxKeywordLength {
		return ErrKeywordTooLong
	}

	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM keyword_subscriptions
			WHERE user_id = ? AND keyword = ? AND is_active = true
		)`, userID, keyword,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("user: check duplicate keyword: %w", err)
	}
	if exists {
		return ErrDuplicateKeyword
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO keyword_subscriptions (user_id, keyword, context_note) VALUES (?, ?, ?)`,
		userID, keyword, req.ContextNote,
	); err != nil {
		return fmt.Errorf("user: insert keyword_subscriptions: %w", err)
	}

	return nil
}

// DeleteKeywordSubscription soft-deletes subID (is_active = false), which
// must be owned by userID.
func (s *UserService) DeleteKeywordSubscription(userID, subID uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	var ownerID uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM keyword_subscriptions WHERE id = ?`, subID,
	).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSubscriptionNotFound
		}
		return fmt.Errorf("user: check keyword subscription ownership: %w", err)
	}
	if ownerID != userID {
		return ErrSubscriptionNotFound
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE keyword_subscriptions SET is_active = false WHERE id = ?`, subID,
	); err != nil {
		return fmt.Errorf("user: soft delete keyword_subscriptions: %w", err)
	}

	return nil
}

// NotificationLog mirrors a row in the notification_logs table.
type NotificationLog struct {
	ID             uint64
	NotifType      string
	AssetSymbol    *string
	Keyword        *string
	ContentSummary string
	SentAt         time.Time
	Status         string
}

// GetNotificationHistory returns userID's notification log, most recent
// first, filtered by notifType ("asset", "sentinel", or "" for all),
// paginated by limit/offset. It also returns the total matching row count
// (ignoring limit/offset) for pagination metadata.
func (s *UserService) GetNotificationHistory(userID uint64, limit, offset int, notifType string) ([]NotificationLog, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	whereClause := "WHERE user_id = ?"
	args := []interface{}{userID}
	if notifType != "" {
		whereClause += " AND notif_type = ?"
		args = append(args, notifType)
	}

	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM notification_logs %s", whereClause)
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("user: count notification_logs: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, notif_type, asset_symbol, keyword, content_summary, sent_at, status
		FROM notification_logs
		%s
		ORDER BY sent_at DESC
		LIMIT ? OFFSET ?`, whereClause)
	queryArgs := append(append([]interface{}{}, args...), limit, offset)

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("user: query notification_logs: %w", err)
	}
	defer rows.Close()

	logs := make([]NotificationLog, 0)
	for rows.Next() {
		var (
			l           NotificationLog
			assetSymbol sql.NullString
			keyword     sql.NullString
		)
		if err := rows.Scan(&l.ID, &l.NotifType, &assetSymbol, &keyword, &l.ContentSummary, &l.SentAt, &l.Status); err != nil {
			return nil, 0, fmt.Errorf("user: scan notification_logs: %w", err)
		}
		if assetSymbol.Valid {
			l.AssetSymbol = &assetSymbol.String
		}
		if keyword.Valid {
			l.Keyword = &keyword.String
		}
		logs = append(logs, l)
	}

	return logs, total, rows.Err()
}

// GetLastAlertTimestamps returns the most recent successfully-sent asset
// and sentinel alert timestamps for userID, either of which may be nil if
// no such alert has ever been sent.
func (s *UserService) GetLastAlertTimestamps(userID uint64) (lastAsset, lastSentinel *time.Time, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	var assetTime, sentinelTime sql.NullTime

	if err := s.db.QueryRowContext(ctx, `
		SELECT MAX(sent_at) FROM notification_logs WHERE user_id = ? AND notif_type = 'asset' AND status = 'sent'`,
		userID,
	).Scan(&assetTime); err != nil {
		return nil, nil, fmt.Errorf("user: query last asset alert: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT MAX(sent_at) FROM notification_logs WHERE user_id = ? AND notif_type = 'sentinel' AND status = 'sent'`,
		userID,
	).Scan(&sentinelTime); err != nil {
		return nil, nil, fmt.Errorf("user: query last sentinel alert: %w", err)
	}

	if assetTime.Valid {
		lastAsset = &assetTime.Time
	}
	if sentinelTime.Valid {
		lastSentinel = &sentinelTime.Time
	}

	return lastAsset, lastSentinel, nil
}

// MarketData is a market_cache snapshot row.
type MarketData struct {
	Symbol       string
	PriceUSD     float64
	PriceIDR     float64
	ChangePct24h float64
	LastFetched  time.Time
	Source       string
}

// GetMarketSnapshot returns cached market data for every symbol currently
// tracked by at least one active asset subscription, system-wide.
func (s *UserService) GetMarketSnapshot() ([]MarketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT mc.symbol, mc.price_usd, mc.price_idr, mc.change_pct_24h, mc.last_fetched, mc.source
		FROM market_cache mc
		WHERE mc.symbol IN (
			SELECT DISTINCT asset_symbol FROM asset_subscriptions WHERE is_active = true
		)
		ORDER BY mc.symbol`,
	)
	if err != nil {
		return nil, fmt.Errorf("user: query market_cache: %w", err)
	}
	defer rows.Close()

	data := make([]MarketData, 0)
	for rows.Next() {
		var m MarketData
		if err := rows.Scan(&m.Symbol, &m.PriceUSD, &m.PriceIDR, &m.ChangePct24h, &m.LastFetched, &m.Source); err != nil {
			return nil, fmt.Errorf("user: scan market_cache: %w", err)
		}
		data = append(data, m)
	}

	return data, rows.Err()
}
