package core_ffi

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	coreDll *syscall.LazyDLL

	procHashSha256Hex       *syscall.LazyProc
	procFreeRustString      *syscall.LazyProc
	procFreeRustBytes       *syscall.LazyProc
	procFfiEncryptAesGcm    *syscall.LazyProc
	procFfiDecryptAesGcm    *syscall.LazyProc
	procDeriveKeyArgon2Ffi  *syscall.LazyProc
)

func init() {
	// Khởi tạo connection với DLL của Rust
	dllPath := filepath.Join("..", "supportflast_core", "target", "release", "supportflast_core.dll")
	coreDll = syscall.NewLazyDLL(dllPath)

	procHashSha256Hex = coreDll.NewProc("hash_sha256_hex")
	procFreeRustString = coreDll.NewProc("free_rust_string")
	procFreeRustBytes = coreDll.NewProc("free_rust_bytes")
	procFfiEncryptAesGcm = coreDll.NewProc("ffi_encrypt_aes_gcm")
	procFfiDecryptAesGcm = coreDll.NewProc("ffi_decrypt_aes_gcm")
	procDeriveKeyArgon2Ffi = coreDll.NewProc("derive_key_argon2_ffi")
}

// ByteArrayToString converts a null-terminated C string pointer to a Go string
func cStringToGoString(cStr uintptr) string {
	if cStr == 0 {
		return ""
	}
	
	var bytes []byte
	for i := 0; ; i++ {
		b := *(*byte)(unsafe.Pointer(cStr + uintptr(i)))
		if b == 0 {
			break
		}
		bytes = append(bytes, b)
	}
	return string(bytes)
}

func HashSha256Hex(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty data")
	}

	ret, _, _ := procHashSha256Hex.Call(
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
	)

	if ret == 0 {
		return "", fmt.Errorf("hash failed")
	}

	// Đọc chuỗi C
	hashStr := cStringToGoString(ret)

	// BẮT BUỘC: Giải phóng bộ nhớ từ Rust theo Rule 7.1
	procFreeRustString.Call(ret)

	return hashStr, nil
}

func EncryptAesGcm(plaintext []byte, key []byte) ([]byte, error) {
	if len(plaintext) == 0 || len(key) != 32 {
		return nil, fmt.Errorf("invalid inputs")
	}

	var outLen, outCap uintptr

	ret, _, _ := procFfiEncryptAesGcm.Call(
		uintptr(unsafe.Pointer(&plaintext[0])),
		uintptr(len(plaintext)),
		uintptr(unsafe.Pointer(&key[0])),
		uintptr(len(key)),
		uintptr(unsafe.Pointer(&outLen)),
		uintptr(unsafe.Pointer(&outCap)),
	)

	if ret == 0 {
		return nil, fmt.Errorf("encryption failed")
	}

	// Chép dữ liệu từ con trỏ Rust sang Go byte slice
	result := make([]byte, outLen)
	// copy từ ret với độ dài outLen
	for i := 0; i < int(outLen); i++ {
		result[i] = *(*byte)(unsafe.Pointer(ret + uintptr(i)))
	}

	// BẮT BUỘC: Giải phóng bộ nhớ từ Rust
	procFreeRustBytes.Call(ret, outLen, outCap)

	return result, nil
}

func DecryptAesGcm(ciphertext []byte, key []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(key) != 32 {
		return nil, fmt.Errorf("invalid inputs")
	}

	var outLen, outCap uintptr

	ret, _, _ := procFfiDecryptAesGcm.Call(
		uintptr(unsafe.Pointer(&ciphertext[0])),
		uintptr(len(ciphertext)),
		uintptr(unsafe.Pointer(&key[0])),
		uintptr(len(key)),
		uintptr(unsafe.Pointer(&outLen)),
		uintptr(unsafe.Pointer(&outCap)),
	)

	if ret == 0 {
		return nil, fmt.Errorf("decryption failed")
	}

	result := make([]byte, outLen)
	for i := 0; i < int(outLen); i++ {
		result[i] = *(*byte)(unsafe.Pointer(ret + uintptr(i)))
	}

	// BẮT BUỘC: Giải phóng bộ nhớ từ Rust
	procFreeRustBytes.Call(ret, outLen, outCap)

	return result, nil
}

func DeriveKeyArgon2(passphrase string, salt []byte) ([]byte, error) {
	if len(passphrase) == 0 || len(salt) < 8 {
		return nil, fmt.Errorf("invalid input")
	}
	
	// Convert Go string to null-terminated C string
	passBytes := append([]byte(passphrase), 0)
	
	outKey := make([]byte, 32)
	
	ret, _, _ := procDeriveKeyArgon2Ffi.Call(
		uintptr(unsafe.Pointer(&passBytes[0])),
		uintptr(unsafe.Pointer(&salt[0])),
		uintptr(len(salt)),
		uintptr(unsafe.Pointer(&outKey[0])),
	)
	
	// ret == 0 means success
	if int32(ret) != 0 {
		return nil, fmt.Errorf("derive key failed with code: %d", int32(ret))
	}
	
	return outKey, nil
}
