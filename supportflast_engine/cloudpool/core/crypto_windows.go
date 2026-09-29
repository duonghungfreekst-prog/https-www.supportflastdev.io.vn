//go:build windows

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

var (
	rustDLLOnce    sync.Once
	rustDLL        *syscall.LazyDLL
	procDeriveKey  *syscall.LazyProc
	procEncrypt    *syscall.LazyProc
	procDecrypt    *syscall.LazyProc
	procHashChunk  *syscall.LazyProc
	procFreeBuffer *syscall.LazyProc
	procFreeString *syscall.LazyProc

	// PQC Procedures từ supportflast_core.dll (Kyber-768 & Dilithium & Hybrid PQC)
	procPqcKyberKeygen       *syscall.LazyProc
	procPqcKyberEncapsulate  *syscall.LazyProc
	procPqcKyberDecapsulate  *syscall.LazyProc
	procPqcDilithiumKeygen   *syscall.LazyProc
	procPqcDilithiumSign     *syscall.LazyProc
	procPqcDilithiumVerify   *syscall.LazyProc
	procPqcHybridEncryptJSON *syscall.LazyProc
	procPqcHybridDecryptJSON *syscall.LazyProc

	procMoveMemory = syscall.NewLazyDLL("ntdll.dll").NewProc("RtlMoveMemory")
	dllLoaded      bool
)

func initRustDLL() {
	rustDLLOnce.Do(func() {
		// Xác định thư mục thực thi của ứng dụng và thư mục làm việc hiện tại
		var execDir string
		if execPath, err := os.Executable(); err == nil {
			execDir = filepath.Dir(execPath)
		}
		cwd, _ := os.Getwd()

		// Danh sách ưu tiên tìm kiếm DLL chính 'supportflast_core.dll'
		// Tuyệt đối xóa bỏ hoàn toàn phụ thuộc vào 'cloudpool_core.dll' cũ
		candidates := []string{
			// 1. Thư mục hiện tại hoặc cùng file binary thực thi
			"supportflast_core.dll",
			filepath.Join(execDir, "supportflast_core.dll"),
			filepath.Join(cwd, "supportflast_core.dll"),

			// 2. Thư mục gốc dự án hoặc thư mục cha
			filepath.Join(cwd, "..", "supportflast_core.dll"),
			filepath.Join(cwd, "..", "..", "supportflast_core.dll"),
			filepath.Join(execDir, "..", "supportflast_core.dll"),

			// 3. Target release build của supportflast_core
			filepath.Join(cwd, "..", "supportflast_core", "target", "release", "supportflast_core.dll"),
			filepath.Join(cwd, "..", "..", "supportflast_core", "target", "release", "supportflast_core.dll"),
			filepath.Join(execDir, "..", "supportflast_core", "target", "release", "supportflast_core.dll"),
			filepath.Join(cwd, "supportflast_core", "target", "release", "supportflast_core.dll"),

			// 4. Đường dẫn tuyệt đối chuẩn của dự án supportflast.dev
			`F:\supportflast.dev\supportflast_core.dll`,
			`F:\supportflast.dev\supportflast_core\target\release\supportflast_core.dll`,
			`F:\supportflast.dev\supportflast_engine\supportflast_core.dll`,
			`F:\supportflast.dev\supportflast_engine\cloudpool\supportflast_core.dll`,
		}

		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				rustDLL = syscall.NewLazyDLL(path)
				procDeriveKey = rustDLL.NewProc("derive_key_ffi")
				procEncrypt = rustDLL.NewProc("encrypt_chunk_ffi")
				procDecrypt = rustDLL.NewProc("decrypt_chunk_ffi")
				procHashChunk = rustDLL.NewProc("hash_chunk_ffi")
				procFreeBuffer = rustDLL.NewProc("free_rust_buffer")
				procFreeString = rustDLL.NewProc("free_rust_string")

				// PQC Procedures
				procPqcKyberKeygen = rustDLL.NewProc("ffi_pqc_kyber_keygen")
				procPqcKyberEncapsulate = rustDLL.NewProc("ffi_pqc_kyber_encapsulate")
				procPqcKyberDecapsulate = rustDLL.NewProc("ffi_pqc_kyber_decapsulate")
				procPqcDilithiumKeygen = rustDLL.NewProc("ffi_pqc_dilithium_keygen")
				procPqcDilithiumSign = rustDLL.NewProc("ffi_pqc_dilithium_sign")
				procPqcDilithiumVerify = rustDLL.NewProc("ffi_pqc_dilithium_verify")
				procPqcHybridEncryptJSON = rustDLL.NewProc("ffi_pqc_hybrid_encrypt_json")
				procPqcHybridDecryptJSON = rustDLL.NewProc("ffi_pqc_hybrid_decrypt_json")

				if err := procDeriveKey.Find(); err == nil {
					dllLoaded = true
					break
				}
			}
		}
	})
}

// goStringAndFree sao chép C-string null-terminated sang Go string và gọi free_rust_string (Rule PHAN 7.1)
func goStringAndFree(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	var strLen int
	for *(*byte)(unsafe.Pointer(ptr + uintptr(strLen))) != 0 {
		strLen++
	}
	bytes := make([]byte, strLen)
	if strLen > 0 {
		procMoveMemory.Call(uintptr(unsafe.Pointer(&bytes[0])), ptr, uintptr(strLen))
	}
	procFreeString.Call(ptr)
	return string(bytes)
}

// goBytesAndFree sao chép buffer từ Rust sang Go slice và giải phóng Rust buffer (Rule PHAN 7.1)
func goBytesAndFree(ptr uintptr, len, cap uintptr) []byte {
	if ptr == 0 {
		return nil
	}
	if len == 0 {
		procFreeBuffer.Call(ptr, len, cap)
		return []byte{}
	}
	bytes := make([]byte, int(len))
	procMoveMemory.Call(uintptr(unsafe.Pointer(&bytes[0])), ptr, len)
	procFreeBuffer.Call(ptr, len, cap)
	return bytes
}

func tryRustDeriveKey(passphrase string, outKey *[32]byte) bool {
	initRustDLL()
	if !dllLoaded {
		return false
	}
	cPass, err := syscall.BytePtrFromString(passphrase)
	if err != nil {
		return false
	}
	r1, _, _ := procDeriveKey.Call(uintptr(unsafe.Pointer(cPass)), uintptr(unsafe.Pointer(&outKey[0])))
	return r1 == 0
}

func tryRustEncrypt(key [32]byte, plaintext []byte) ([]byte, bool) {
	initRustDLL()
	if !dllLoaded || len(plaintext) == 0 {
		return nil, false
	}
	var outLen, outCap uintptr
	ptr, _, _ := procEncrypt.Call(
		uintptr(unsafe.Pointer(&key[0])),
		uintptr(unsafe.Pointer(&plaintext[0])),
		uintptr(len(plaintext)),
		uintptr(unsafe.Pointer(&outLen)),
		uintptr(unsafe.Pointer(&outCap)),
	)
	if ptr != 0 && outLen > 0 {
		return goBytesAndFree(ptr, outLen, outCap), true
	}
	return nil, false
}

func tryRustDecrypt(key [32]byte, encryptedData []byte) ([]byte, bool) {
	initRustDLL()
	if !dllLoaded || len(encryptedData) < 28 {
		return nil, false
	}
	var outLen, outCap uintptr
	ptr, _, _ := procDecrypt.Call(
		uintptr(unsafe.Pointer(&key[0])),
		uintptr(unsafe.Pointer(&encryptedData[0])),
		uintptr(len(encryptedData)),
		uintptr(unsafe.Pointer(&outLen)),
		uintptr(unsafe.Pointer(&outCap)),
	)
	if ptr != 0 {
		return goBytesAndFree(ptr, outLen, outCap), true
	}
	return nil, false
}

func tryRustHashChunk(data []byte) (string, bool) {
	initRustDLL()
	if !dllLoaded || len(data) == 0 {
		return "", false
	}
	ptr, _, _ := procHashChunk.Call(
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
	)
	if ptr != 0 {
		return goStringAndFree(ptr), true
	}
	return "", false
}

type pqcKeypairRaw struct {
	PublicKey []byte `json:"public_key"`
	SecretKey []byte `json:"secret_key"`
}

func tryRustPqcKyberKeygen() ([]byte, []byte, bool) {
	initRustDLL()
	if !dllLoaded || procPqcKyberKeygen == nil || procPqcKyberKeygen.Find() != nil {
		return nil, nil, false
	}
	ptr, _, _ := procPqcKyberKeygen.Call()
	if ptr == 0 {
		return nil, nil, false
	}
	jsonStr := goStringAndFree(ptr)
	var kp pqcKeypairRaw
	if err := json.Unmarshal([]byte(jsonStr), &kp); err != nil {
		return nil, nil, false
	}
	return kp.PublicKey, kp.SecretKey, true
}

func tryRustPqcKyberEncapsulate(pk []byte) ([]byte, []byte, bool) {
	initRustDLL()
	if !dllLoaded || len(pk) == 0 || procPqcKyberEncapsulate == nil || procPqcKyberEncapsulate.Find() != nil {
		return nil, nil, false
	}
	var ctLen, ctCap, ssLen, ssCap uintptr
	var ssPtr uintptr

	ctPtr, _, _ := procPqcKyberEncapsulate.Call(
		uintptr(unsafe.Pointer(&pk[0])),
		uintptr(len(pk)),
		uintptr(unsafe.Pointer(&ctLen)),
		uintptr(unsafe.Pointer(&ctCap)),
		uintptr(unsafe.Pointer(&ssLen)),
		uintptr(unsafe.Pointer(&ssCap)),
		uintptr(unsafe.Pointer(&ssPtr)),
	)
	if ctPtr == 0 || ssPtr == 0 {
		return nil, nil, false
	}
	ct := goBytesAndFree(ctPtr, ctLen, ctCap)
	ss := goBytesAndFree(ssPtr, ssLen, ssCap)
	return ct, ss, true
}

func tryRustPqcKyberDecapsulate(sk []byte, ct []byte) ([]byte, bool) {
	initRustDLL()
	if !dllLoaded || len(sk) == 0 || len(ct) == 0 || procPqcKyberDecapsulate == nil || procPqcKyberDecapsulate.Find() != nil {
		return nil, false
	}
	var ssLen, ssCap uintptr
	ssPtr, _, _ := procPqcKyberDecapsulate.Call(
		uintptr(unsafe.Pointer(&sk[0])),
		uintptr(len(sk)),
		uintptr(unsafe.Pointer(&ct[0])),
		uintptr(len(ct)),
		uintptr(unsafe.Pointer(&ssLen)),
		uintptr(unsafe.Pointer(&ssCap)),
	)
	if ssPtr == 0 {
		return nil, false
	}
	ss := goBytesAndFree(ssPtr, ssLen, ssCap)
	return ss, true
}

func tryRustPqcDilithiumKeygen() ([]byte, []byte, bool) {
	initRustDLL()
	if !dllLoaded || procPqcDilithiumKeygen == nil || procPqcDilithiumKeygen.Find() != nil {
		return nil, nil, false
	}
	ptr, _, _ := procPqcDilithiumKeygen.Call()
	if ptr == 0 {
		return nil, nil, false
	}
	jsonStr := goStringAndFree(ptr)
	var kp pqcKeypairRaw
	if err := json.Unmarshal([]byte(jsonStr), &kp); err != nil {
		return nil, nil, false
	}
	return kp.PublicKey, kp.SecretKey, true
}

func tryRustPqcDilithiumSign(sk []byte, msg []byte) ([]byte, bool) {
	initRustDLL()
	if !dllLoaded || len(sk) == 0 || len(msg) == 0 || procPqcDilithiumSign == nil || procPqcDilithiumSign.Find() != nil {
		return nil, false
	}
	var sigLen, sigCap uintptr
	sigPtr, _, _ := procPqcDilithiumSign.Call(
		uintptr(unsafe.Pointer(&sk[0])),
		uintptr(len(sk)),
		uintptr(unsafe.Pointer(&msg[0])),
		uintptr(len(msg)),
		uintptr(unsafe.Pointer(&sigLen)),
		uintptr(unsafe.Pointer(&sigCap)),
	)
	if sigPtr == 0 {
		return nil, false
	}
	sig := goBytesAndFree(sigPtr, sigLen, sigCap)
	return sig, true
}

func tryRustPqcDilithiumVerify(pk []byte, msg []byte, sig []byte) (bool, bool) {
	initRustDLL()
	if !dllLoaded || len(pk) == 0 || len(msg) == 0 || len(sig) == 0 || procPqcDilithiumVerify == nil || procPqcDilithiumVerify.Find() != nil {
		return false, false
	}
	ret, _, _ := procPqcDilithiumVerify.Call(
		uintptr(unsafe.Pointer(&pk[0])),
		uintptr(len(pk)),
		uintptr(unsafe.Pointer(&msg[0])),
		uintptr(len(msg)),
		uintptr(unsafe.Pointer(&sig[0])),
		uintptr(len(sig)),
	)
	return ret == 1, true
}

func tryRustPqcHybridEncrypt(recipientKyberPKHex string, plaintext string) (string, bool) {
	initRustDLL()
	if !dllLoaded || recipientKyberPKHex == "" || plaintext == "" || procPqcHybridEncryptJSON == nil || procPqcHybridEncryptJSON.Find() != nil {
		return "", false
	}
	cPK, err1 := syscall.BytePtrFromString(recipientKyberPKHex)
	cPT, err2 := syscall.BytePtrFromString(plaintext)
	if err1 != nil || err2 != nil {
		return "", false
	}
	ptr, _, _ := procPqcHybridEncryptJSON.Call(
		uintptr(unsafe.Pointer(cPK)),
		uintptr(unsafe.Pointer(cPT)),
	)
	if ptr == 0 {
		return "", false
	}
	return goStringAndFree(ptr), true
}

func tryRustPqcHybridDecrypt(recipientKyberSKHex string, envelopeJSON string) (string, bool) {
	initRustDLL()
	if !dllLoaded || recipientKyberSKHex == "" || envelopeJSON == "" || procPqcHybridDecryptJSON == nil || procPqcHybridDecryptJSON.Find() != nil {
		return "", false
	}
	cSK, err1 := syscall.BytePtrFromString(recipientKyberSKHex)
	cEnv, err2 := syscall.BytePtrFromString(envelopeJSON)
	if err1 != nil || err2 != nil {
		return "", false
	}
	ptr, _, _ := procPqcHybridDecryptJSON.Call(
		uintptr(unsafe.Pointer(cSK)),
		uintptr(unsafe.Pointer(cEnv)),
	)
	if ptr == 0 {
		return "", false
	}
	return goStringAndFree(ptr), true
}

func isDLLLoaded() bool {
	initRustDLL()
	return dllLoaded
}
