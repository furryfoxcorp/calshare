// Package secret encrypts small values at rest with AES-256-GCM under the
// server's data key. It is used for upstream feed credentials.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

// ErrKeySize is returned when the key is not 32 bytes.
var ErrKeySize = errors.New("secret: key must be 32 bytes")

// Encrypt seals plaintext with key, returning nonce-prefixed ciphertext. An
// empty plaintext returns nil so callers can store NULL.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens ciphertext produced by Encrypt. Empty input returns empty.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("secret: ciphertext too short")
	}
	nonce, body := ciphertext[:ns], ciphertext[ns:]
	return gcm.Open(nil, nonce, body, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
