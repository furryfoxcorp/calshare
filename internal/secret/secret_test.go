package secret

import (
	"bytes"
	"testing"
)

func key32() []byte { return bytes.Repeat([]byte{0x7}, 32) }

func TestEncryptDecryptRoundtrip(t *testing.T) {
	k := key32()
	ct, err := Encrypt(k, []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("hunter2")) {
		t.Error("plaintext visible in ciphertext")
	}
	pt, err := Decrypt(k, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "hunter2" {
		t.Errorf("got %q", pt)
	}
}

func TestEmptyIsNil(t *testing.T) {
	ct, err := Encrypt(key32(), nil)
	if err != nil || ct != nil {
		t.Errorf("empty encrypt = %v, %v", ct, err)
	}
	pt, err := Decrypt(key32(), nil)
	if err != nil || pt != nil {
		t.Errorf("empty decrypt = %v, %v", pt, err)
	}
}

func TestWrongKeyFails(t *testing.T) {
	ct, _ := Encrypt(key32(), []byte("secret"))
	other := bytes.Repeat([]byte{0x9}, 32)
	if _, err := Decrypt(other, ct); err == nil {
		t.Error("decrypt with wrong key should fail")
	}
}

func TestBadKeySize(t *testing.T) {
	if _, err := Encrypt([]byte("short"), []byte("x")); err != ErrKeySize {
		t.Errorf("err = %v, want ErrKeySize", err)
	}
}
