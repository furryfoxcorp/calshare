package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u, _ := db.UpsertUserOnLogin(ctx, "s", "s@example.com", "S")

	sess, err := db.CreateSession(ctx, u.ID, time.Hour, "TestAgent", "1.2.3.4")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("empty session id")
	}

	got, err := db.SessionByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if got.UserID != u.ID {
		t.Errorf("user id = %d, want %d", got.UserID, u.ID)
	}

	if err := db.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionByID(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session still found: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u, _ := db.UpsertUserOnLogin(ctx, "s2", "s2@example.com", "S2")

	sess, _ := db.CreateSession(ctx, u.ID, -time.Minute, "", "")
	if _, err := db.SessionByID(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session returned: %v", err)
	}
}

func TestSessionTouchSlidesExpiry(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u, _ := db.UpsertUserOnLogin(ctx, "s3", "s3@example.com", "S3")
	sess, _ := db.CreateSession(ctx, u.ID, time.Minute, "", "")
	orig := sess.ExpiresAt

	time.Sleep(2 * time.Millisecond)
	if err := db.TouchSession(ctx, sess.ID, time.Hour, "5.6.7.8"); err != nil {
		t.Fatal(err)
	}
	got, _ := db.SessionByID(ctx, sess.ID)
	if !got.ExpiresAt.After(orig) {
		t.Errorf("expiry did not slide: %v <= %v", got.ExpiresAt, orig)
	}
}

func TestOIDCFlowTakeIsOneShot(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	flow := OIDCFlow{State: "abc", CodeVerifier: "verifier", Nonce: "nonce", RedirectTo: "/dashboard"}
	if err := db.CreateOIDCFlow(ctx, flow, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := db.TakeOIDCFlow(ctx, "abc")
	if err != nil {
		t.Fatalf("TakeOIDCFlow: %v", err)
	}
	if got.CodeVerifier != "verifier" || got.RedirectTo != "/dashboard" {
		t.Errorf("flow mismatch: %+v", got)
	}
	if _, err := db.TakeOIDCFlow(ctx, "abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("flow should be one-shot, got %v", err)
	}
}
