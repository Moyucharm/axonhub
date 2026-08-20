package biz

import (
	"strings"
	"testing"
)

func TestCPASecretEncryptionRoundTrip(t *testing.T) {
	t.Parallel()

	ciphertext, err := encryptCPASecret("system-jwt-secret", "cpa-management-key")
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if !strings.HasPrefix(ciphertext, cpaSecretCipherVersion+".") {
		t.Fatalf("ciphertext does not contain version prefix: %q", ciphertext)
	}
	if strings.Contains(ciphertext, "cpa-management-key") {
		t.Fatal("ciphertext contains plaintext secret")
	}

	plaintext, err := decryptCPASecret("system-jwt-secret", ciphertext)
	if err != nil {
		t.Fatalf("decrypt secret: %v", err)
	}
	if plaintext != "cpa-management-key" {
		t.Fatalf("unexpected plaintext: %q", plaintext)
	}
}

func TestCPASecretEncryptionRejectsWrongKeyAndTampering(t *testing.T) {
	t.Parallel()

	ciphertext, err := encryptCPASecret("system-jwt-secret", "cpa-management-key")
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if _, err := decryptCPASecret("different-system-secret", ciphertext); err == nil {
		t.Fatal("expected wrong-key decryption to fail")
	}

	parts := strings.Split(ciphertext, ".")
	if len(parts) != 3 || len(parts[2]) == 0 {
		t.Fatalf("unexpected ciphertext format: %q", ciphertext)
	}
	replacement := "A"
	if strings.HasPrefix(parts[2], replacement) {
		replacement = "B"
	}
	parts[2] = replacement + parts[2][1:]
	tampered := strings.Join(parts, ".")
	if _, err := decryptCPASecret("system-jwt-secret", tampered); err == nil {
		t.Fatal("expected tampered ciphertext decryption to fail")
	}
}
