package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
)

// keyLen is the byte length of the session and data keys.
const keyLen = 32

// SessionKey returns the persisted session signing key. If override is
// non-nil it is stored and returned. Otherwise the stored key is returned, or
// a fresh random key is generated, persisted, and returned on first run.
func (db *DB) SessionKey(ctx context.Context, override []byte) ([]byte, error) {
	return db.persistedKey(ctx, "session_key", override)
}

// DataKey behaves like SessionKey for the at-rest encryption key.
func (db *DB) DataKey(ctx context.Context, override []byte) ([]byte, error) {
	return db.persistedKey(ctx, "data_key", override)
}

func (db *DB) persistedKey(ctx context.Context, column string, override []byte) ([]byte, error) {
	// The settings row is single-row (id = 1). Make sure it exists.
	if _, err := db.ExecContext(ctx,
		"INSERT OR IGNORE INTO settings(id, created_at) VALUES (1, ?)", nowUTC()); err != nil {
		return nil, err
	}

	if override != nil {
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf("UPDATE settings SET %s = ? WHERE id = 1", column), override); err != nil {
			return nil, err
		}
		return override, nil
	}

	var stored []byte
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT %s FROM settings WHERE id = 1", column)).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if len(stored) > 0 {
		return stored, nil
	}

	fresh := make([]byte, keyLen)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("UPDATE settings SET %s = ? WHERE id = 1", column), fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}
