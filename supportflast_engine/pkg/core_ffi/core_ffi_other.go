//go:build !windows

package core_ffi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// HashSha256Hex tính chuỗi hex SHA-256 từ dữ liệu nhị phân (native fallback cho môi trường không phải Windows)
func HashSha256Hex(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty data")
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// EncryptAesGcm mã hóa plaintext bằng AES-256-GCM với 12-byte Nonce ngẫu nhiên
// Định dạng output: [12-byte Nonce][Ciphertext + 16-byte Tag] khớp với Rust supportflast_core
func EncryptAesGcm(plaintext []byte, key []byte) ([]byte, error) {
	if len(plaintext) == 0 || len(key) != 32 {
		return nil, fmt.Errorf("invalid inputs")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Gắn nonce vào đầu ciphertext
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// DecryptAesGcm giải mã ciphertext bằng AES-256-GCM
// Nhận vào [12-byte Nonce][Ciphertext + 16-byte Tag] và khóa 32 bytes
func DecryptAesGcm(ciphertext []byte, key []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(key) != 32 {
		return nil, fmt.Errorf("invalid inputs")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// DeriveKeyArgon2 dẫn xuất khóa 32 bytes từ passphrase và salt bằng Argon2id
// Tham số: time=2, memory=19456 KiB, threads=1, keyLen=32 bytes (đồng bộ 100% với Argon2::default() của Rust Core)
func DeriveKeyArgon2(passphrase string, salt []byte) ([]byte, error) {
	if len(passphrase) == 0 || len(salt) < 8 {
		return nil, fmt.Errorf("invalid input")
	}

	// Argon2id v19: time=2, memory=19456 KiB, threads=1, keyLen=32
	key := argon2.IDKey([]byte(passphrase), salt, 2, 19456, 1, 32)
	return key, nil
}
