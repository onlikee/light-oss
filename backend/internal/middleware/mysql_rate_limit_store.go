package middleware

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

const mysqlRateLimitCleanupBatchSize = 256

type RateLimitStoreStats struct {
	Entries            int
	MaxEntries         int
	ExpiredEvictions   uint64
	CapacityRejections uint64
}

type MySQLRateLimitStore struct {
	db          *sql.DB
	maxEntries  int
	cleanupMu   sync.Mutex
	nextCleanup time.Time
}

func NewMySQLRateLimitStore(db *sql.DB, maxEntries int) *MySQLRateLimitStore {
	return &MySQLRateLimitStore{
		db:          db,
		maxEntries:  maxEntries,
		nextCleanup: time.Now().Add(time.Minute),
	}
}

func (s *MySQLRateLimitStore) Allow(
	ctx context.Context,
	key string,
	rps float64,
	burst int,
	ttl time.Duration,
) (bool, error) {
	if rps <= 0 || burst <= 0 || ttl <= 0 || s.maxEntries <= 0 {
		return false, fmt.Errorf("invalid rate limit policy")
	}
	keyHash := sha256.Sum256([]byte(key))

	allowed, found, err := s.updateExisting(ctx, keyHash[:], rps, burst, ttl)
	if err != nil {
		return false, err
	}
	if found {
		if err := s.cleanupExpired(ctx, ttl); err != nil {
			return false, err
		}
		return allowed, nil
	}

	allowed, err = s.insertBounded(ctx, keyHash[:], rps, burst, ttl)
	if err != nil {
		return false, err
	}
	if err := s.cleanupExpired(ctx, ttl); err != nil {
		return false, err
	}
	return allowed, nil
}

func (s *MySQLRateLimitStore) updateExisting(
	ctx context.Context,
	keyHash []byte,
	rps float64,
	burst int,
	ttl time.Duration,
) (allowed bool, found bool, resultErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("begin rate limit transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && resultErr == nil && rollbackErr != sql.ErrTxDone {
			resultErr = fmt.Errorf("roll back rate limit transaction: %w", rollbackErr)
			allowed = false
			found = false
		}
	}()

	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM rate_limit_buckets
		WHERE key_hash = ?
		FOR UPDATE`, keyHash).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("lock rate limit bucket: %w", err)
	}

	if err := updateRateLimitBucket(ctx, tx, keyHash, rps, burst, ttl); err != nil {
		return false, false, err
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT last_allowed
		FROM rate_limit_buckets
		WHERE key_hash = ?`, keyHash).Scan(&allowed); err != nil {
		return false, false, fmt.Errorf("read rate limit decision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("commit rate limit bucket: %w", err)
	}
	return allowed, true, nil
}

func (s *MySQLRateLimitStore) insertBounded(
	ctx context.Context,
	keyHash []byte,
	rps float64,
	burst int,
	ttl time.Duration,
) (allowed bool, resultErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin bounded rate limit transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && resultErr == nil && rollbackErr != sql.ErrTxDone {
			resultErr = fmt.Errorf("roll back bounded rate limit transaction: %w", rollbackErr)
			allowed = false
		}
	}()

	entryCount, err := lockRateLimitCapacity(ctx, tx)
	if err != nil {
		return false, err
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM rate_limit_buckets
		WHERE key_hash = ?
		FOR UPDATE`, keyHash).Scan(&exists); err == nil {
		if err := updateRateLimitBucket(ctx, tx, keyHash, rps, burst, ttl); err != nil {
			return false, err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT last_allowed
			FROM rate_limit_buckets
			WHERE key_hash = ?`, keyHash).Scan(&allowed); err != nil {
			return false, fmt.Errorf("read concurrent rate limit decision: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit concurrent rate limit bucket: %w", err)
		}
		return allowed, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("recheck rate limit bucket: %w", err)
	}

	if entryCount >= int64(s.maxEntries) {
		deleted, err := cleanupExpiredRateLimitBuckets(ctx, tx)
		if err != nil {
			return false, err
		}
		entryCount -= deleted
	}
	if entryCount >= int64(s.maxEntries) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE rate_limit_capacity
			SET capacity_rejections = capacity_rejections + 1
			WHERE id = 1`); err != nil {
			return false, fmt.Errorf("record rate limit capacity rejection: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit rate limit capacity rejection: %w", err)
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rate_limit_buckets (
			key_hash,
			tokens,
			last_refill_at,
			expires_at,
			last_allowed
		) VALUES (?, ?, UTC_TIMESTAMP(6), TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)), TRUE)`,
		keyHash,
		float64(burst-1),
		ttl.Microseconds(),
	); err != nil {
		return false, fmt.Errorf("insert rate limit bucket: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE rate_limit_capacity
		SET entry_count = entry_count + 1
		WHERE id = 1`); err != nil {
		return false, fmt.Errorf("increment rate limit entry count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit new rate limit bucket: %w", err)
	}
	return true, nil
}

func updateRateLimitBucket(
	ctx context.Context,
	tx *sql.Tx,
	keyHash []byte,
	rps float64,
	burst int,
	ttl time.Duration,
) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE rate_limit_buckets
		SET
			last_allowed = (
				expires_at <= UTC_TIMESTAMP(6)
				OR LEAST(
					?,
					tokens + GREATEST(TIMESTAMPDIFF(MICROSECOND, last_refill_at, UTC_TIMESTAMP(6)), 0) * ? / 1000000
				) >= 1
			),
			tokens = CASE
				WHEN expires_at <= UTC_TIMESTAMP(6) THEN ? - 1
				ELSE LEAST(
					?,
					tokens + GREATEST(TIMESTAMPDIFF(MICROSECOND, last_refill_at, UTC_TIMESTAMP(6)), 0) * ? / 1000000
				) - IF(
					LEAST(
						?,
						tokens + GREATEST(TIMESTAMPDIFF(MICROSECOND, last_refill_at, UTC_TIMESTAMP(6)), 0) * ? / 1000000
					) >= 1,
					1,
					0
				)
			END,
			last_refill_at = UTC_TIMESTAMP(6),
			expires_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6))
		WHERE key_hash = ?`,
		burst,
		rps,
		burst,
		burst,
		rps,
		burst,
		rps,
		ttl.Microseconds(),
		keyHash,
	); err != nil {
		return fmt.Errorf("update rate limit bucket: %w", err)
	}
	return nil
}

func lockRateLimitCapacity(ctx context.Context, tx *sql.Tx) (int64, error) {
	var entryCount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT entry_count
		FROM rate_limit_capacity
		WHERE id = 1
		FOR UPDATE`).Scan(&entryCount); err != nil {
		return 0, fmt.Errorf("lock rate limit capacity: %w", err)
	}
	return entryCount, nil
}

func cleanupExpiredRateLimitBuckets(ctx context.Context, tx *sql.Tx) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		DELETE FROM rate_limit_buckets
		WHERE expires_at <= UTC_TIMESTAMP(6)
		ORDER BY expires_at
		LIMIT ?`, mysqlRateLimitCleanupBatchSize)
	if err != nil {
		return 0, fmt.Errorf("delete expired rate limit buckets: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expired rate limit cleanup result: %w", err)
	}
	if deleted == 0 {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE rate_limit_capacity
		SET
			entry_count = GREATEST(entry_count - ?, 0),
			expired_evictions = expired_evictions + ?
		WHERE id = 1`, deleted, deleted); err != nil {
		return 0, fmt.Errorf("decrement rate limit entry count: %w", err)
	}
	return deleted, nil
}

func (s *MySQLRateLimitStore) cleanupExpired(ctx context.Context, ttl time.Duration) error {
	now := time.Now()
	cleanupInterval := time.Minute
	if ttl < cleanupInterval {
		cleanupInterval = ttl
	}

	s.cleanupMu.Lock()
	if now.Before(s.nextCleanup) {
		s.cleanupMu.Unlock()
		return nil
	}
	s.nextCleanup = now.Add(cleanupInterval)
	s.cleanupMu.Unlock()

	_, err := s.CleanupExpired(ctx)
	return err
}

func (s *MySQLRateLimitStore) CleanupExpired(ctx context.Context) (deleted int64, resultErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin rate limit cleanup transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && resultErr == nil && rollbackErr != sql.ErrTxDone {
			resultErr = fmt.Errorf("roll back rate limit cleanup transaction: %w", rollbackErr)
			deleted = 0
		}
	}()

	if _, err := lockRateLimitCapacity(ctx, tx); err != nil {
		return 0, err
	}
	deleted, err = cleanupExpiredRateLimitBuckets(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit rate limit cleanup: %w", err)
	}
	return deleted, nil
}

func (s *MySQLRateLimitStore) Stats(ctx context.Context) (RateLimitStoreStats, error) {
	stats := RateLimitStoreStats{MaxEntries: s.maxEntries}
	if err := s.db.QueryRowContext(ctx, `
		SELECT entry_count, expired_evictions, capacity_rejections
		FROM rate_limit_capacity
		WHERE id = 1`).Scan(
		&stats.Entries,
		&stats.ExpiredEvictions,
		&stats.CapacityRejections,
	); err != nil {
		return RateLimitStoreStats{}, fmt.Errorf("read rate limit capacity stats: %w", err)
	}
	return stats, nil
}
