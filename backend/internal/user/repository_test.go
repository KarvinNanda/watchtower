package user_test

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/karvin-nanda/watchtower/internal/user"
)

func newMockRepository(t *testing.T) (*user.Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return user.NewRepository(db), mock
}

var repoUserColumns = []string{
	"id", "email", "password_hash", "telegram_asset_chat_id", "telegram_sentinel_chat_id",
	"alert_cooldown_hours", "preferred_language", "is_active",
}

func TestRepository_Register_Success(t *testing.T) {
	t.Parallel()
	repo, mock := newMockRepository(t)

	mock.ExpectExec(`INSERT INTO users`).
		WithArgs("repo@example.com", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectQuery(`SELECT`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows(repoUserColumns).
			AddRow(7, "repo@example.com", "hash", nil, nil, 4, "id", true))

	u, err := repo.Register(context.Background(), "repo@example.com", "whatever-password")

	require.NoError(t, err)
	assert.Equal(t, uint64(7), u.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_Register_DuplicateEmail(t *testing.T) {
	t.Parallel()
	repo, mock := newMockRepository(t)

	mock.ExpectExec(`INSERT INTO users`).
		WithArgs("dup@example.com", sqlmock.AnyArg()).
		WillReturnError(&mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry"})

	_, err := repo.Register(context.Background(), "dup@example.com", "whatever-password")

	assert.ErrorIs(t, err, user.ErrEmailTaken)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_Authenticate_Success(t *testing.T) {
	t.Parallel()
	repo, mock := newMockRepository(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("Correct1!"), bcrypt.DefaultCost)
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT`).
		WithArgs("auth@example.com").
		WillReturnRows(sqlmock.NewRows(repoUserColumns).
			AddRow(3, "auth@example.com", string(hash), nil, nil, 4, "id", true))

	u, err := repo.Authenticate(context.Background(), "auth@example.com", "Correct1!")
	require.NoError(t, err)
	assert.Equal(t, uint64(3), u.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_Authenticate_WrongPassword(t *testing.T) {
	t.Parallel()
	repo, mock := newMockRepository(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("Correct1!"), bcrypt.DefaultCost)
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT`).
		WithArgs("auth2@example.com").
		WillReturnRows(sqlmock.NewRows(repoUserColumns).
			AddRow(3, "auth2@example.com", string(hash), nil, nil, 4, "id", true))

	_, err = repo.Authenticate(context.Background(), "auth2@example.com", "WrongPassword!")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_Authenticate_NotFound(t *testing.T) {
	t.Parallel()
	repo, mock := newMockRepository(t)

	mock.ExpectQuery(`SELECT`).
		WithArgs("nobody@example.com").
		WillReturnError(sql.ErrNoRows)

	_, err := repo.Authenticate(context.Background(), "nobody@example.com", "whatever")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)
	require.NoError(t, mock.ExpectationsWereMet())
}
