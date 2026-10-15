//go:build integration

// Migration tests against a real MySQL 8. Run with:
//
//	MIGRATION_TEST_DSN='root:<password>@tcp(127.0.0.1:3306)/' go test -tags integration ./internal/db/ -count=1
//
// The DSN user needs CREATE/DROP DATABASE. Each run creates and drops its own
// database, so it never touches application data.
package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMigrationDB(t *testing.T) (*sql.DB, *migrate.Migrate) {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DSN not set")
	}
	admin, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "wt_migtest_" + hex.EncodeToString(b)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + name) })

	conn, err := sql.Open("mysql", dsn+name+"?parseTime=true&loc=UTC")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	driver, err := mysqlmigrate.WithInstance(conn, &mysqlmigrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://../../migrations", "mysql", driver)
	require.NoError(t, err)
	return conn, m
}

func count(t *testing.T, db *sql.DB, query string, args ...interface{}) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(query, args...).Scan(&n))
	return n
}

// TestMigrations_LegacyDataUpgradeAndRollback seeds data the way the
// pre-Spec-2 schema (version 9) stored it, then checks every Spec 2 migration.
func TestMigrations_LegacyDataUpgradeAndRollback(t *testing.T) {
	db, m := newMigrationDB(t)
	require.NoError(t, m.Migrate(9))

	for _, q := range []string{
		`INSERT INTO users (id, email, password_hash) VALUES (1, 'mig@test.local', 'x')`,
		`INSERT INTO asset_subscriptions (user_id, asset_type, asset_symbol, alert_type, price_lower_usd, price_upper_usd, pct_change_threshold, is_active) VALUES
			(1, 'crypto', 'BTC',   'price_threshold', 90000,   NULL,    NULL, TRUE),
			(1, 'crypto', 'BTC',   'price_threshold', 90000,   NULL,    NULL, FALSE),
			(1, 'crypto', 'ETH',   'price_threshold', NULL,    5000,    NULL, TRUE),
			(1, 'stock',  'AAPL',  'price_threshold', 200,     400,     NULL, TRUE),
			(1, 'crypto', 'SOL',   'pct_change',      NULL,    NULL,    5,    TRUE),
			(1, 'gold',   'ANTAM', 'both',            2000000, 2600000, 2,    FALSE),
			(1, 'gold',   'XAU',   'price_threshold', NULL,    3000000, NULL, TRUE),
			(1, 'crypto', 'XRP',   'both',            NULL,    NULL,    NULL, TRUE)`,
		`INSERT INTO market_cache (symbol, price_usd, price_idr, change_pct_24h, last_fetched, source) VALUES
			('ANTAM', 146, 2392157, 0, NOW(), 'harga-emas.org'),
			('BTC', 81714, 1300000000, 1.5, NOW(), 'coingecko')`,
	} {
		_, err := db.Exec(q)
		require.NoError(t, err, q)
	}

	require.NoError(t, m.Up())

	// 000011: 8 subscriptions -> 9 rules. The duplicate BTC row collapses into
	// one active rule, "both" splits into up to three, all-NULL XRP is dropped.
	assert.Equal(t, 9, count(t, db, `SELECT COUNT(*) FROM asset_rules`))
	assert.Equal(t, 9, count(t, db, `SELECT COUNT(*) FROM asset_rules WHERE baseline_pending = TRUE`))
	assert.Equal(t, 1, count(t, db, `SELECT COUNT(*) FROM asset_rules WHERE asset_symbol = 'BTC' AND is_active = TRUE`))
	assert.Equal(t, 3, count(t, db, `SELECT COUNT(*) FROM asset_rules WHERE asset_symbol = 'ANTAM' AND is_active = FALSE`))
	assert.Equal(t, 0, count(t, db, `SELECT COUNT(*) FROM asset_rules WHERE asset_symbol = 'XRP'`))
	// 000014: the XAU rule now targets ANTAM.
	assert.Equal(t, 0, count(t, db, `SELECT COUNT(*) FROM asset_rules WHERE asset_symbol = 'XAU'`))
	assert.Equal(t, 1, count(t, db, `SELECT COUNT(*) FROM asset_rules WHERE asset_symbol = 'ANTAM' AND direction = 'above' AND level_usd = 3000000`))
	// 000013 + 000015: unknown gold change is NULL, other changes are kept.
	assert.Equal(t, 1, count(t, db, `SELECT COUNT(*) FROM market_cache WHERE symbol = 'ANTAM' AND change_pct_24h IS NULL`))
	assert.Equal(t, 1, count(t, db, `SELECT COUNT(*) FROM market_cache WHERE symbol = 'BTC' AND change_pct_24h = 1.5`))

	// Roll back every Spec 2 migration (15 -> 9) with a NULL change present,
	// then upgrade again from the untouched legacy tables.
	require.NoError(t, m.Steps(-6))
	assert.Equal(t, 0, count(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('asset_rules', 'price_snapshots')`))
	assert.Equal(t, 8, count(t, db, `SELECT COUNT(*) FROM asset_subscriptions`), "legacy rows are never touched")

	err := m.Up()
	require.True(t, err == nil || errors.Is(err, migrate.ErrNoChange), "%v", err)
	assert.Equal(t, 9, count(t, db, `SELECT COUNT(*) FROM asset_rules`))
}
