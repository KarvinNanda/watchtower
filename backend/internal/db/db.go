// Package db wires up the MySQL connection pool and Redis client shared
// across the WatchTower binaries, plus migration and health-check helpers.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/redis/go-redis/v9"

	"github.com/karvin-nanda/watchtower/internal/config"
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 5
	connMaxLifetime = 5 * time.Minute
	pingTimeout     = 5 * time.Second
)

// DB bundles the MySQL and Redis clients used across WatchTower.
type DB struct {
	SQL   *sql.DB
	Redis *redis.Client
}

// New opens the MySQL connection pool and Redis client described by cfg and
// verifies both are reachable before returning.
func New(cfg *config.Config) (*DB, error) {
	sqlDB, err := sql.Open("mysql", cfg.Database.DSN())
	if err != nil {
		return nil, fmt.Errorf("db: open mysql: %w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: ping mysql: %w", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
	})

	if err := redisClient.Ping(ctx).Err(); err != nil {
		_ = sqlDB.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("db: ping redis: %w", err)
	}

	return &DB{SQL: sqlDB, Redis: redisClient}, nil
}

// Close releases the underlying MySQL and Redis connections.
func (d *DB) Close() error {
	var errs []error
	if err := d.SQL.Close(); err != nil {
		errs = append(errs, fmt.Errorf("db: close mysql: %w", err))
	}
	if err := d.Redis.Close(); err != nil {
		errs = append(errs, fmt.Errorf("db: close redis: %w", err))
	}
	return errors.Join(errs...)
}

// RunMigrations applies all pending golang-migrate migrations located at
// migrationsPath (e.g. "migrations") against the connected MySQL database.
func (d *DB) RunMigrations(migrationsPath string) error {
	driver, err := mysqlmigrate.WithInstance(d.SQL, &mysqlmigrate.Config{})
	if err != nil {
		return fmt.Errorf("db: create migrate driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", migrationsPath),
		"mysql",
		driver,
	)
	if err != nil {
		return fmt.Errorf("db: create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: run migrations: %w", err)
	}

	return nil
}

// HealthCheck verifies that both MySQL and Redis are reachable.
func (d *DB) HealthCheck(ctx context.Context) error {
	if err := d.SQL.PingContext(ctx); err != nil {
		return fmt.Errorf("db: mysql health check failed: %w", err)
	}
	if err := d.Redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("db: redis health check failed: %w", err)
	}
	return nil
}
