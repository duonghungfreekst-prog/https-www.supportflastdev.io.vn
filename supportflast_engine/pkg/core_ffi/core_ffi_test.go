package core_ffi

import (
	"bytes"
	"testing"
)

func TestHashSha256Hex(t *testing.T) {
	data := []byte("hello world")
	hash, err := HashSha256Hex(data)
	if err != nil {
		t.Fatalf("Failed to hash: %v", err)
	}
	
	expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if hash != expected {
		t.Errorf("Expected %s but got %s", expected, hash)
	}
}

func TestEncryptDecryptAesGcm(t *testing.T) {
	key := []byte("01234567890123456789012345678901") // 32 bytes
	plaintext := []byte("Secret message for FFI test")

	encrypted, err := EncryptAesGcm(plaintext, key)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}

	if len(encrypted) == 0 {
		t.Fatalf("Encrypted data is empty")
	}

	decrypted, err := DecryptAesGcm(encrypted, key)
	if err != nil {
		t.Fatalf("Failed to decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("Decrypted data does not match original plaintext")
	}
}

func TestDeriveKeyArgon2(t *testing.T) {
	passphrase := "my_secure_password"
	salt := []byte("1234567890123456") // 16 bytes
	
	key, err := DeriveKeyArgon2(passphrase, salt)
	if err != nil {
		t.Fatalf("Failed to derive key: %v", err)
	}
	
	if len(key) != 32 {
		t.Errorf("Expected key length 32, got %d", len(key))
	}
}
