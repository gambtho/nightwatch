// Package vault is the envelope-encryption core: master key -> per-tenant
// KEK -> per-secret DEK, all AES-256-GCM. It is pure crypto — no database,
// no HTTP. Persistence lives in store; composition for the proxy lives in
// proxyadapter. Decrypted values exist only on the proxy's request path.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const KeyLen = 32

type Master struct {
	key []byte
}

func NewMaster(key []byte) (*Master, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("vault: master key must be %d bytes, got %d", KeyLen, len(key))
	}
	return &Master{key: append([]byte(nil), key...)}, nil
}

// seal encrypts plaintext under key; the random nonce is prefixed to the
// returned blob so a single []byte column can hold it.
func seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, plaintext, nil)...), nil
}

func open(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("vault: blob too short")
	}
	return gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewTenantKEK mints a fresh tenant key-encryption key, returned wrapped
// under the master. The plaintext KEK never leaves this package.
func (m *Master) NewTenantKEK() ([]byte, error) {
	kek := make([]byte, KeyLen)
	if _, err := io.ReadFull(rand.Reader, kek); err != nil {
		return nil, err
	}
	return seal(m.key, kek)
}

// EncryptSecret generates a fresh DEK, wraps it under the tenant KEK, and
// encrypts value under the DEK with an explicit nonce (stored separately,
// matching the connection table's columns).
func (m *Master) EncryptSecret(wrappedKEK []byte, value string) (dekWrapped, ciphertext, nonce []byte, err error) {
	kek, err := open(m.key, wrappedKEK)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("vault: unwrap kek: %w", err)
	}
	dek := make([]byte, KeyLen)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, nil, nil, err
	}
	if dekWrapped, err = seal(kek, dek); err != nil {
		return nil, nil, nil, err
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, nil, err
	}
	return dekWrapped, gcm.Seal(nil, nonce, []byte(value), nil), nonce, nil
}

func (m *Master) DecryptSecret(wrappedKEK, dekWrapped, ciphertext, nonce []byte) (string, error) {
	kek, err := open(m.key, wrappedKEK)
	if err != nil {
		return "", fmt.Errorf("vault: unwrap kek: %w", err)
	}
	dek, err := open(kek, dekWrapped)
	if err != nil {
		return "", fmt.Errorf("vault: unwrap dek: %w", err)
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("vault: invalid nonce length %d", len(nonce))
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("vault: decrypt: %w", err)
	}
	return string(plain), nil
}
