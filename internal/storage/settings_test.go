package storage

import (
	"bytes"
	"context"
	"testing"
)

func TestSessionKeyGeneratesAndPersists(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	first, err := db.SessionKey(ctx, nil)
	if err != nil {
		t.Fatalf("first SessionKey: %v", err)
	}
	if len(first) != keyLen {
		t.Fatalf("key length = %d, want %d", len(first), keyLen)
	}

	second, err := db.SessionKey(ctx, nil)
	if err != nil {
		t.Fatalf("second SessionKey: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("SessionKey returned a different key on the second call")
	}
}

func TestDataKeyOverridePersists(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	override := bytes.Repeat([]byte{0xAB}, keyLen)
	got, err := db.DataKey(ctx, override)
	if err != nil {
		t.Fatalf("DataKey override: %v", err)
	}
	if !bytes.Equal(got, override) {
		t.Fatal("override not returned")
	}

	stored, err := db.DataKey(ctx, nil)
	if err != nil {
		t.Fatalf("DataKey nil: %v", err)
	}
	if !bytes.Equal(stored, override) {
		t.Fatal("override not persisted for later nil read")
	}
}

func TestSessionAndDataKeyAreIndependent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	sk, _ := db.SessionKey(ctx, nil)
	dk, _ := db.DataKey(ctx, nil)
	if bytes.Equal(sk, dk) {
		t.Fatal("session and data keys should differ")
	}
}
