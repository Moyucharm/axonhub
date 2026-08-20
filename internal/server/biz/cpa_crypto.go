// Package-level note: the CPA encryption key is derived from the system JWT
// secret (HKDF-SHA256). Rotating the JWT secret invalidates every stored CPA
// management secret — there is no key-versioning fallback yet. After a
// rotation, re-enter each instance's management secret via updateCPAInstance;
// the empty-secret edit path preserves the stored ciphertext.
package biz

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	cpaSecretCipherVersion = "v1"
	cpaSecretKeyInfo       = "axonhub/cpa/management-secret/v1"
)

func deriveCPAEncryptionKey(systemSecret string) ([]byte, error) {
	if strings.TrimSpace(systemSecret) == "" {
		return nil, fmt.Errorf("system secret is empty")
	}

	key := make([]byte, 32)
	reader := hkdf.New(sha256.New, []byte(systemSecret), nil, []byte(cpaSecretKeyInfo))
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive CPA encryption key: %w", err)
	}
	return key, nil
}

func encryptCPASecret(systemSecret, plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("CPA management secret is empty")
	}

	key, err := deriveCPAEncryptionKey(systemSecret)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create CPA secret cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create CPA secret GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate CPA secret nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(cpaSecretKeyInfo))

	return strings.Join([]string{
		cpaSecretCipherVersion,
		base64.RawURLEncoding.EncodeToString(nonce),
		base64.RawURLEncoding.EncodeToString(ciphertext),
	}, "."), nil
}

func decryptCPASecret(systemSecret, encoded string) (string, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 || parts[0] != cpaSecretCipherVersion {
		return "", fmt.Errorf("unsupported CPA secret ciphertext format")
	}

	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode CPA secret nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decode CPA secret ciphertext: %w", err)
	}

	key, err := deriveCPAEncryptionKey(systemSecret)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create CPA secret cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create CPA secret GCM: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("invalid CPA secret nonce length")
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(cpaSecretKeyInfo))
	if err != nil {
		return "", fmt.Errorf("decrypt CPA management secret: %w", err)
	}
	return string(plaintext), nil
}
