package storage

import "context"

// CountViews returns how many views a user has defined. The full views
// repository lands with the views subsystem; this is enough for the dashboard.
func (db *DB) CountViews(ctx context.Context, userID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM views WHERE user_id = ?", userID).Scan(&n)
	return n, err
}
