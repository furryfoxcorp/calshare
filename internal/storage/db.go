// Package storage owns the SQLite database: connection setup, schema
// migrations, and the repositories that read and write domain rows.
package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQL handle plus the repository methods defined across this
// package.
type DB struct {
	*sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the
// pragmas the server depends on: WAL journaling, NORMAL sync, foreign keys,
// and a busy timeout. The caller owns Close.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", path)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc.org/sqlite applies _pragma DSN options per connection, but a
	// pool can hand out fresh connections later, so cap the pool and verify
	// the critical pragmas on the connection we will keep warm.
	sqldb.SetMaxOpenConns(1)
	db := &DB{sqldb}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("check foreign_keys pragma: %w", err)
	}
	if fk != 1 {
		sqldb.Close()
		return nil, fmt.Errorf("foreign_keys pragma not enabled (got %d)", fk)
	}
	return db, nil
}

// now returns the current UTC time formatted as the project stores it.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
