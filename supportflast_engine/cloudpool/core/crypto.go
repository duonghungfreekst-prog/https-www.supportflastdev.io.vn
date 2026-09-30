package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// DeriveKey derives a 32-byte AES key from a passphrase using Rust supportflast_core FFI with fallback
func DeriveKey(passphrase string, salt []byte) [32]byte {
	var outKey [32]byte
	if tryRustDeriveKey(passphrase, &outKey) {
		return outKey
	}

	// Go Native Fallback
	h := sha256.New()
	h.Write([]byte(passphrase))
	if len(salt) > 0 {
		h.Write(salt)
	} else {
		h.Write([]byte("cloudpool_default_salt_2026"))
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

// EncryptChunk encrypts a byte slice using hardware-accelerated AES-256-GCM via supportflast_core.dll
// Output: [12-byte Nonce] + [Ciphertext + 16-byte Tag]
func EncryptChunk(key [32]byte, plaintext []byte) ([]byte, error) {
	if encSlice, ok := tryRustEncrypt(key, plaintext); ok {
		return encSlice, nil
	}

	// Go Native Fallback
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	result := make([]byte, len(nonce)+len(ciphertext))
	copy(result, nonce)
	copy(result[len(nonce):], ciphertext)
	return result, nil
}

// DecryptChunk decrypts an AES-256-GCM encrypted byte slice via supportflast_core.dll with Go fallback
func DecryptChunk(key [32]byte, encryptedData []byte) ([]byte, error) {
	if len(encryptedData) < 28 { // 12 nonce + 16 tag
		return nil, errors.New("encrypted chunk is too short")
	}

	// 1. Uu tien Zero-Copy In-Place (0 cap phat, 0 memcpy, cuc nhanh)
	if decSlice, ok := tryRustDecryptInPlace(key, encryptedData); ok {
		return decSlice, nil
	}

	// 2. Fallback sang FFI cu (neu DLL chua co ham in-place hoac OS khac)
	if decSlice, ok := tryRustDecrypt(key, encryptedData); ok {
		return decSlice, nil
	}

	// Go Native Fallback
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(encryptedData) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := encryptedData[:nonceSize], encryptedData[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (corrupted or wrong key): %w", err)
	}
	return plaintext, nil
}

// HashSHA256 returns hex string of SHA256 using supportflast_core FFI with Go fallback
func HashSHA256(data []byte) string {
	if hash, ok := tryRustHashChunk(data); ok {
		return hash
	}
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// HashPasswordBcrypt hashes a password using bcrypt
func HashPasswordBcrypt(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

// CheckPasswordHashBcrypt compares a bcrypt password hash with its possible plaintext equivalent
func CheckPasswordHashBcrypt(password, hash string) bool {
	// Backward compatibility for old SHA-256 hashes
	if len(hash) == 64 && hash[0] != '$' {
		oldHash := HashSHA256([]byte(password))
		return subtle.ConstantTimeCompare([]byte(hash), []byte(oldHash)) == 1
	}

	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// EncryptSecret encrypts a string secret and returns "ENC:" + hex ciphertext
func EncryptSecret(key [32]byte, plaintext string) string {
	if plaintext == "" {
		return ""
	}
	if len(plaintext) > 4 && plaintext[:4] == "ENC:" {
		return plaintext
	}
	enc, err := EncryptChunk(key, []byte(plaintext))
	if err != nil {
		return plaintext
	}
	return "ENC:" + hex.EncodeToString(enc)
}

// DecryptSecret decrypts a string secret prefixed with "ENC:"
func DecryptSecret(key [32]byte, ciphertext string) string {
	if len(ciphertext) < 4 || ciphertext[:4] != "ENC:" {
		return ciphertext
	}
	rawHex := ciphertext[4:]
	data, err := hex.DecodeString(rawHex)
	if err != nil {
		return ciphertext
	}
	dec, err := DecryptChunk(key, data)
	if err != nil {
		return ciphertext
	}
	return string(dec)
}

// ConstantTimeCompare compares two strings in constant time (Rule PHAN 3.6)
func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BlindIndexHash generates a deterministic hash for searchable encrypted fields
func BlindIndexHash(key [32]byte, plaintext string) string {
	if plaintext == "" {
		return ""
	}
	lowerText := strings.ToLower(strings.TrimSpace(plaintext))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(lowerText))
	return hex.EncodeToString(mac.Sum(nil))
}

// IsDLLLoaded returns whether Rust core DLL is currently loaded
func IsDLLLoaded() bool {
	return isDLLLoaded()
}

// =========================================================================
// POST-QUANTUM CRYPTOGRAPHY (PQC) APIS - SUPPORTFLAST_CORE FFI INTEGRATION
// =========================================================================

// PQCKyberKeygen generates a post-quantum Kyber-768 KEM keypair
func PQCKyberKeygen() (publicKey, secretKey []byte, err error) {
	pk, sk, ok := tryRustPqcKyberKeygen()
	if !ok {
		return nil, nil, errors.New("PQC Kyber keypair generation failed or supportflast_core.dll unavailable")
	}
	return pk, sk, nil
}

// PQCEncapsulate encapsulates a shared secret using recipient's Kyber-768 public key
func PQCEncapsulate(publicKey []byte) (ciphertext, sharedSecret []byte, err error) {
	ct, ss, ok := tryRustPqcKyberEncapsulate(publicKey)
	if !ok {
		return nil, nil, errors.New("PQC Kyber encapsulation failed")
	}
	return ct, ss, nil
}

// PQCDecapsulate recovers the shared secret using recipient's Kyber-768 secret key
func PQCDecapsulate(secretKey, ciphertext []byte) (sharedSecret []byte, err error) {
	ss, ok := tryRustPqcKyberDecapsulate(secretKey, ciphertext)
	if !ok {
		return nil, errors.New("PQC Kyber decapsulation failed")
	}
	return ss, nil
}

// PQCDilithiumKeygen generates a post-quantum Dilithium digital signature keypair
func PQCDilithiumKeygen() (publicKey, secretKey []byte, err error) {
	pk, sk, ok := tryRustPqcDilithiumKeygen()
	if !ok {
		return nil, nil, errors.New("PQC Dilithium keypair generation failed or supportflast_core.dll unavailable")
	}
	return pk, sk, nil
}

// PQCSign signs a message using Dilithium secret key
func PQCSign(secretKey, message []byte) ([]byte, error) {
	sig, ok := tryRustPqcDilithiumSign(secretKey, message)
	if !ok {
		return nil, errors.New("PQC Dilithium signature failed")
	}
	return sig, nil
}

// PQCVerify verifies a Dilithium signature against a message and public key
func PQCVerify(publicKey, message, signature []byte) bool {
	valid, ok := tryRustPqcDilithiumVerify(publicKey, message, signature)
	if !ok {
		return false
	}
	return valid
}

// PQCHybridEncrypt encrypts plaintext with Hybrid Kyber-768 + Hardware AES-256-GCM
func PQCHybridEncrypt(recipientKyberPKHex, plaintext string) (string, error) {
	envJSON, ok := tryRustPqcHybridEncrypt(recipientKyberPKHex, plaintext)
	if !ok {
		return "", errors.New("PQC Hybrid encryption failed")
	}
	return envJSON, nil
}

// PQCHybridDecrypt decrypts a Hybrid Kyber-768 + Hardware AES-256-GCM envelope
func PQCHybridDecrypt(recipientKyberSKHex, envelopeJSON string) (string, error) {
	pt, ok := tryRustPqcHybridDecrypt(recipientKyberSKHex, envelopeJSON)
	if !ok {
		return "", errors.New("PQC Hybrid decryption failed")
	}
	return pt, nil
}
