package core

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestNativeCryptoRoundtrip(t *testing.T) {
	key := DeriveKey("master_passphrase_test_2026", nil)
	plaintext := []byte("CloudPool Enterprise Multi-Drive Zero-Knowledge Storage Test Payload 1234567890!")

	enc, err := EncryptChunk(key, plaintext)
	if err != nil {
		t.Fatalf("EncryptChunk failed: %v", err)
	}

	if len(enc) <= len(plaintext) {
		t.Fatalf("Encrypted payload length too small: %d", len(enc))
	}

	dec, err := DecryptChunk(key, enc)
	if err != nil {
		t.Fatalf("DecryptChunk failed: %v", err)
	}

	if !bytes.Equal(plaintext, dec) {
		t.Fatalf("Decrypted plaintext does not match original: got %s, want %s", string(dec), string(plaintext))
	}
}

func TestSecretEncryption(t *testing.T) {
	key := DeriveKey("secret_key_passphrase", nil)
	secret := `{"access_token":"ya29.test_token_secret_12345","refresh_token":"1//test_refresh"}`

	encSecret := EncryptSecret(key, secret)
	if encSecret == secret {
		t.Fatalf("Secret was not encrypted: %s", encSecret)
	}

	decSecret := DecryptSecret(key, encSecret)
	if decSecret != secret {
		t.Fatalf("Decrypted secret does not match original: got %s, want %s", decSecret, secret)
	}
}

func TestSHA256Hash(t *testing.T) {
	data := []byte("test_sha256_payload")
	hash := HashSHA256(data)
	if len(hash) != 64 {
		t.Fatalf("Expected 64-char hex hash, got %d chars: %s", len(hash), hash)
	}
}

func TestSupportFlastCoreDLLLoaded(t *testing.T) {
	loaded := IsDLLLoaded()
	if !loaded {
		t.Fatalf("Expected supportflast_core.dll to be loaded, but it was not found")
	}
}

func TestPQCKyberRoundtrip(t *testing.T) {
	pk, sk, err := PQCKyberKeygen()
	if err != nil {
		t.Fatalf("PQCKyberKeygen failed: %v", err)
	}
	if len(pk) == 0 || len(sk) == 0 {
		t.Fatalf("PQCKyberKeygen returned empty keys")
	}

	ct, ssEnc, err := PQCEncapsulate(pk)
	if err != nil {
		t.Fatalf("PQCEncapsulate failed: %v", err)
	}
	if len(ct) == 0 || len(ssEnc) != 32 {
		t.Fatalf("Invalid ciphertext or shared secret length: ct=%d, ss=%d", len(ct), len(ssEnc))
	}

	ssDec, err := PQCDecapsulate(sk, ct)
	if err != nil {
		t.Fatalf("PQCDecapsulate failed: %v", err)
	}

	if !bytes.Equal(ssEnc, ssDec) {
		t.Fatalf("Shared secrets do not match! Enc=%x, Dec=%x", ssEnc, ssDec)
	}
}

func TestPQCDilithiumSignature(t *testing.T) {
	pk, sk, err := PQCDilithiumKeygen()
	if err != nil {
		t.Fatalf("PQCDilithiumKeygen failed: %v", err)
	}
	if len(pk) == 0 || len(sk) == 0 {
		t.Fatalf("PQCDilithiumKeygen returned empty keys")
	}

	msg := []byte("CloudPool Quantum-Secure File Sync Audit Ticket #8812")
	sig, err := PQCSign(sk, msg)
	if err != nil {
		t.Fatalf("PQCSign failed: %v", err)
	}
	if len(sig) == 0 {
		t.Fatalf("PQCSign returned empty signature")
	}

	valid := PQCVerify(pk, msg, sig)
	if !valid {
		t.Fatalf("PQCVerify failed for authentic message")
	}

	tamperedMsg := []byte("CloudPool Tampered File Sync Audit Ticket #8812")
	if PQCVerify(pk, tamperedMsg, sig) {
		t.Fatalf("PQCVerify should have rejected tampered message")
	}
}

func TestPQCHybridEnvelope(t *testing.T) {
	pk, sk, err := PQCKyberKeygen()
	if err != nil {
		t.Fatalf("PQCKyberKeygen failed: %v", err)
	}

	pkHex := hex.EncodeToString(pk)
	skHex := hex.EncodeToString(sk)
	plaintext := "Sensitive CloudPool Multi-Drive Manifest Configuration v1.0"

	envJSON, err := PQCHybridEncrypt(pkHex, plaintext)
	if err != nil {
		t.Fatalf("PQCHybridEncrypt failed: %v", err)
	}
	if len(envJSON) == 0 {
		t.Fatalf("PQCHybridEncrypt returned empty envelope")
	}

	decrypted, err := PQCHybridDecrypt(skHex, envJSON)
	if err != nil {
		t.Fatalf("PQCHybridDecrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Decrypted plaintext does not match: got %s, want %s", decrypted, plaintext)
	}
}
