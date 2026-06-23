package storage

import (
	"context"
	"errors"
	"testing"
)

func TestUpsertUserOnLogin(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	u, err := db.UpsertUserOnLogin(ctx, "sub-1", "Owner@Example.com", "Owner")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if u.ID == 0 || u.Email != "Owner@Example.com" || u.DisplayName != "Owner" {
		t.Fatalf("unexpected user %+v", u)
	}
	if u.LastLoginAt == nil {
		t.Fatal("last login not set")
	}

	again, err := db.UpsertUserOnLogin(ctx, "sub-1", "owner@example.com", "Owner Renamed")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if again.ID != u.ID {
		t.Fatalf("upsert created a new user (%d != %d)", again.ID, u.ID)
	}
	if again.DisplayName != "Owner Renamed" {
		t.Fatalf("display name not updated: %q", again.DisplayName)
	}
}

func TestUserByEmailCaseInsensitive(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, err := db.UpsertUserOnLogin(ctx, "sub-2", "Zoe@Example.com", "Zoe")
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.UserByEmail(ctx, "zoe@example.com")
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if got.OIDCSub != "sub-2" {
		t.Fatalf("wrong user: %+v", got)
	}
}

func TestUserByEmailNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.UserByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSetAdmin(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	db.UpsertUserOnLogin(ctx, "sub-3", "admin@example.com", "Admin")
	if err := db.SetAdmin(ctx, "admin@example.com", true); err != nil {
		t.Fatalf("SetAdmin: %v", err)
	}
	u, _ := db.UserByEmail(ctx, "admin@example.com")
	if !u.IsAdmin {
		t.Fatal("user not admin after SetAdmin")
	}
}

func TestAppPasswordMatchAndRevoke(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u, _ := db.UpsertUserOnLogin(ctx, "sub-4", "dev@example.com", "Dev")

	id, err := db.CreateAppPassword(ctx, u.ID, "iPhone", "secret-pass-1")
	if err != nil {
		t.Fatalf("CreateAppPassword: %v", err)
	}

	matchedUser, matchedPw, err := db.MatchAppPassword(ctx, "dev@example.com", "secret-pass-1")
	if err != nil {
		t.Fatalf("MatchAppPassword: %v", err)
	}
	if matchedUser.ID != u.ID || matchedPw.ID != id {
		t.Fatalf("wrong match: user %d pw %d", matchedUser.ID, matchedPw.ID)
	}

	if _, _, err := db.MatchAppPassword(ctx, "dev@example.com", "wrong"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong password should not match, got %v", err)
	}

	if err := db.RevokeAppPassword(ctx, id); err != nil {
		t.Fatalf("RevokeAppPassword: %v", err)
	}
	if _, _, err := db.MatchAppPassword(ctx, "dev@example.com", "secret-pass-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked password should not match, got %v", err)
	}
}

func TestAppPasswordsForUser(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u, _ := db.UpsertUserOnLogin(ctx, "sub-5", "list@example.com", "List")
	db.CreateAppPassword(ctx, u.ID, "Mac", "pw-a")
	db.CreateAppPassword(ctx, u.ID, "Phone", "pw-b")
	list, err := db.AppPasswordsForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("AppPasswordsForUser: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d passwords, want 2", len(list))
	}
}
