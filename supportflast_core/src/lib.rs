use aes_gcm::{
    aead::{Aead, KeyInit, OsRng},
    Aes256Gcm, Nonce,
};
use rand::RngCore;
use sha2::{Digest, Sha256};
use std::ffi::{CStr, CString};
use std::os::raw::c_char;
use subtle::ConstantTimeEq;
use argon2::Argon2;

pub mod pqc;
pub use pqc::*;

pub const NONCE_LEN: usize = 12;
pub const KEY_LEN: usize = 32;

/// Tính mã băm SHA-256 an toàn bộ nhớ (zero-heap ngoài kết quả hex trả về)
pub fn calculate_sha256(data: &[u8]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(data);
    let hash = hasher.finalize();
    hex::encode(hash)
}

/// So sánh hai chuỗi bí mật với Constant-Time Comparison chống tấn công Timing Side-Channel.
pub fn constant_time_compare(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.ct_eq(b).into()
}

/// Mã hóa dữ liệu bảo mật (ví dụ API key, thông tin vé nhạy cảm) bằng AES-256-GCM.
pub fn encrypt_support_payload(key: &[u8; KEY_LEN], plaintext: &[u8]) -> Result<Vec<u8>, String> {
    let cipher = Aes256Gcm::new_from_slice(key)
        .map_err(|e| format!("Failed to create cipher: {}", e))?;
    let mut nonce_bytes = [0u8; NONCE_LEN];
    OsRng.fill_bytes(&mut nonce_bytes);
    let nonce = Nonce::from_slice(&nonce_bytes);

    let ciphertext = cipher
        .encrypt(nonce, plaintext)
        .map_err(|e| format!("Encryption error: {}", e))?;

    // Kết hợp Nonce (12 bytes) + Ciphertext (kèm Auth Tag 16 bytes)
    let mut result = Vec::with_capacity(NONCE_LEN + ciphertext.len());
    result.extend_from_slice(&nonce_bytes);
    result.extend_from_slice(&ciphertext);
    Ok(result)
}

/// Giải mã dữ liệu bảo mật bằng AES-256-GCM.
pub fn decrypt_support_payload(key: &[u8; KEY_LEN], payload: &[u8]) -> Result<Vec<u8>, String> {
    if payload.len() < NONCE_LEN {
        return Err("Payload too short for nonce".to_string());
    }

    let (nonce_bytes, ciphertext) = payload.split_at(NONCE_LEN);
    let cipher = Aes256Gcm::new_from_slice(key)
        .map_err(|e| format!("Failed to create cipher: {}", e))?;
    let nonce = Nonce::from_slice(nonce_bytes);

    cipher
        .decrypt(nonce, ciphertext)
        .map_err(|e| format!("Decryption error: {}", e))
}

/// Mã hóa dữ liệu nhạy cảm bằng AES-256-GCM.
/// Nhận plaintext và khóa đối xứng 256-bit (32 bytes).
/// Trả về vector byte chứa Nonce (12 bytes) + Ciphertext (kèm Auth Tag 16 bytes),
/// hoặc Vec rỗng nếu key không đúng 32 bytes hoặc mã hóa thất bại.
pub fn encrypt_aes_gcm(plaintext: &[u8], key: &[u8]) -> Vec<u8> {
    if key.len() != KEY_LEN {
        return Vec::new();
    }
    let mut key_arr = [0u8; KEY_LEN];
    key_arr.copy_from_slice(key);
    encrypt_support_payload(&key_arr, plaintext).unwrap_or_default()
}

/// Giải mã dữ liệu an toàn bằng AES-256-GCM.
/// Nhận ciphertext (Nonce 12 bytes ở đầu) và khóa đối xứng 256-bit (32 bytes).
/// Trả về vector byte chứa plaintext đã giải mã, hoặc Vec rỗng nếu lỗi xác thực / sai key.
pub fn decrypt_aes_gcm(ciphertext: &[u8], key: &[u8]) -> Vec<u8> {
    if key.len() != KEY_LEN || ciphertext.len() < NONCE_LEN {
        return Vec::new();
    }
    let mut key_arr = [0u8; KEY_LEN];
    key_arr.copy_from_slice(key);
    decrypt_support_payload(&key_arr, ciphertext).unwrap_or_default()
}

// ==========================================
// C-ABI EXPORTS (FFI) cho Go Engine & External
// ==========================================

/// Giải phóng bộ nhớ chuỗi do Rust cấp phát cho C/Go
/// BẮT BUỘC theo quy tắc FFI Memory Leak Guard (Rule 7.1)
#[no_mangle]
pub unsafe extern "C" fn free_rust_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        drop(CString::from_raw(ptr));
    }
}

/// Giải phóng vùng nhớ byte buffer do Rust cấp phát cho C/Go
/// BẮT BUỘC theo quy tắc FFI Memory Leak Guard (Rule 7.1)
#[no_mangle]
pub unsafe extern "C" fn free_rust_bytes(ptr: *mut u8, len: usize, cap: usize) {
    if !ptr.is_null() && cap > 0 {
        drop(Vec::from_raw_parts(ptr, len, cap));
    }
}

/// Tính SHA-256 qua FFI C-ABI, trả về C string (phải giải phóng bằng free_rust_string)
#[no_mangle]
pub unsafe extern "C" fn hash_sha256_hex(data: *const u8, len: usize) -> *mut c_char {
    if data.is_null() {
        return std::ptr::null_mut();
    }
    let slice = std::slice::from_raw_parts(data, len);
    let hash = calculate_sha256(slice);
    match CString::new(hash) {
        Ok(c_str) => c_str.into_raw(),
        Err(_) => std::ptr::null_mut(),
    }
}

/// Kiểm tra token xác thực thời gian không đổi qua FFI
#[no_mangle]
pub unsafe extern "C" fn verify_token_constant_time(
    token_a: *const c_char,
    token_b: *const c_char,
) -> i32 {
    if token_a.is_null() || token_b.is_null() {
        return 0;
    }
    let str_a = match CStr::from_ptr(token_a).to_str() {
        Ok(s) => s.as_bytes(),
        Err(_) => return 0,
    };
    let str_b = match CStr::from_ptr(token_b).to_str() {
        Ok(s) => s.as_bytes(),
        Err(_) => return 0,
    };

    if constant_time_compare(str_a, str_b) {
        1
    } else {
        0
    }
}

/// Mã hóa chuỗi text an toàn trả về chuỗi Hex kết hợp nonce
#[no_mangle]
pub unsafe extern "C" fn encrypt_text_hex(
    key_hex: *const c_char,
    plaintext: *const c_char,
) -> *mut c_char {
    if key_hex.is_null() || plaintext.is_null() {
        return std::ptr::null_mut();
    }
    let key_str = match CStr::from_ptr(key_hex).to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    let text = match CStr::from_ptr(plaintext).to_str() {
        Ok(s) => s.as_bytes(),
        Err(_) => return std::ptr::null_mut(),
    };

    let key_bytes = match hex::decode(key_str) {
        Ok(b) if b.len() == KEY_LEN => {
            let mut arr = [0u8; KEY_LEN];
            arr.copy_from_slice(&b);
            arr
        }
        _ => return std::ptr::null_mut(),
    };

    match encrypt_support_payload(&key_bytes, text) {
        Ok(encrypted) => {
            let hex_str = hex::encode(encrypted);
            match CString::new(hex_str) {
                Ok(c_str) => c_str.into_raw(),
                Err(_) => std::ptr::null_mut(),
            }
        }
        Err(_) => std::ptr::null_mut(),
    }
}

/// Giải mã chuỗi Hex (chứa Nonce + Ciphertext) qua FFI C-ABI, trả về C string plaintext (phải giải phóng bằng free_rust_string)
#[no_mangle]
pub unsafe extern "C" fn decrypt_hex_text(
    key_hex: *const c_char,
    ciphertext_hex: *const c_char,
) -> *mut c_char {
    if key_hex.is_null() || ciphertext_hex.is_null() {
        return std::ptr::null_mut();
    }
    let key_str = match CStr::from_ptr(key_hex).to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    let cipher_str = match CStr::from_ptr(ciphertext_hex).to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };

    let key_bytes = match hex::decode(key_str) {
        Ok(b) if b.len() == KEY_LEN => {
            let mut arr = [0u8; KEY_LEN];
            arr.copy_from_slice(&b);
            arr
        }
        _ => return std::ptr::null_mut(),
    };

    let cipher_bytes = match hex::decode(cipher_str) {
        Ok(b) => b,
        Err(_) => return std::ptr::null_mut(),
    };

    match decrypt_support_payload(&key_bytes, &cipher_bytes) {
        Ok(plaintext_bytes) => match CString::new(plaintext_bytes) {
            Ok(c_str) => c_str.into_raw(),
            Err(_) => std::ptr::null_mut(),
        },
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Mã hóa binary AES-GCM
#[no_mangle]
pub unsafe extern "C" fn ffi_encrypt_aes_gcm(
    plaintext: *const u8,
    pt_len: usize,
    key: *const u8,
    key_len: usize,
    out_len: *mut usize,
    out_cap: *mut usize,
) -> *mut u8 {
    if plaintext.is_null() || key.is_null() || out_len.is_null() || out_cap.is_null() {
        return std::ptr::null_mut();
    }
    let pt_slice = std::slice::from_raw_parts(plaintext, pt_len);
    let key_slice = std::slice::from_raw_parts(key, key_len);
    let mut encrypted = encrypt_aes_gcm(pt_slice, key_slice);
    if encrypted.is_empty() {
        *out_len = 0;
        *out_cap = 0;
        return std::ptr::null_mut();
    }
    *out_len = encrypted.len();
    *out_cap = encrypted.capacity();
    let ptr = encrypted.as_mut_ptr();
    std::mem::forget(encrypted);
    ptr
}

/// FFI: Giải mã binary AES-GCM
#[no_mangle]
pub unsafe extern "C" fn ffi_decrypt_aes_gcm(
    ciphertext: *const u8,
    ct_len: usize,
    key: *const u8,
    key_len: usize,
    out_len: *mut usize,
    out_cap: *mut usize,
) -> *mut u8 {
    if ciphertext.is_null() || key.is_null() || out_len.is_null() || out_cap.is_null() {
        return std::ptr::null_mut();
    }
    let ct_slice = std::slice::from_raw_parts(ciphertext, ct_len);
    let key_slice = std::slice::from_raw_parts(key, key_len);
    let mut decrypted = decrypt_aes_gcm(ct_slice, key_slice);
    if decrypted.is_empty() {
        *out_len = 0;
        *out_cap = 0;
        return std::ptr::null_mut();
    }
    *out_len = decrypted.len();
    *out_cap = decrypted.capacity();
    let ptr = decrypted.as_mut_ptr();
    std::mem::forget(decrypted);
    ptr
}

/// FFI: [DEPRECATED] Tạo khóa 256-bit (32 bytes) từ passphrase bằng SHA-256.
/// ⚠️ DEPRECATED: Hàm này dùng SHA-256 + static salt, KHÔNG đủ an toàn cho key derivation.
/// Chỉ giữ lại để backward compatibility. Sử dụng `derive_key_argon2_ffi` thay thế.
#[no_mangle]
pub unsafe extern "C" fn derive_key_ffi(passphrase: *const c_char, out_key: *mut u8) -> i32 {
    if passphrase.is_null() || out_key.is_null() {
        return -1;
    }
    let c_str = match CStr::from_ptr(passphrase).to_str() {
        Ok(s) => s,
        Err(_) => return -2,
    };
    let mut hasher = Sha256::new();
    hasher.update(c_str.as_bytes());
    hasher.update(b"cloudpool_default_salt_2026");
    let result = hasher.finalize();
    std::ptr::copy_nonoverlapping(result.as_ptr(), out_key, KEY_LEN);
    0
}

/// FFI: Tạo khóa 256-bit (32 bytes) từ passphrase bằng Argon2id (memory-hard KDF).
/// Đây là phiên bản an toàn thay thế cho `derive_key_ffi` (SHA-256 DEPRECATED).
/// - salt_ptr: con trỏ tới salt (tối thiểu 16 bytes, do caller tạo bằng crypto RNG)
/// - salt_len: độ dài salt (bytes)
/// - out_key: bộ đệm 32 bytes nhận kết quả derived key
/// Trả về 0 nếu thành công, mã lỗi âm nếu thất bại.
#[no_mangle]
pub unsafe extern "C" fn derive_key_argon2_ffi(
    passphrase: *const c_char,
    salt_ptr: *const u8,
    salt_len: usize,
    out_key: *mut u8,
) -> i32 {
    if passphrase.is_null() || salt_ptr.is_null() || out_key.is_null() {
        return -1;
    }
    if salt_len < 8 {
        return -3; // Salt quá ngắn (tối thiểu 8 bytes, khuyến nghị 16 bytes)
    }
    let c_str = match CStr::from_ptr(passphrase).to_str() {
        Ok(s) => s,
        Err(_) => return -2,
    };
    let salt = std::slice::from_raw_parts(salt_ptr, salt_len);
    let argon2 = Argon2::default(); // Argon2id v19, m=19456 KiB, t=2, p=1
    let mut key_buf = [0u8; KEY_LEN];
    match argon2.hash_password_into(c_str.as_bytes(), salt, &mut key_buf) {
        Ok(_) => {
            std::ptr::copy_nonoverlapping(key_buf.as_ptr(), out_key, KEY_LEN);
            0
        }
        Err(_) => -4, // Argon2 hashing failed
    }
}

/// FFI: Mã hóa khối dữ liệu (Chunk) bằng AES-256-GCM tăng tốc phần cứng
/// Format: Nonce (12 bytes) + Ciphertext + Tag (16 bytes)
#[no_mangle]
pub unsafe extern "C" fn encrypt_chunk_ffi(
    key_ptr: *const u8,
    data_ptr: *const u8,
    data_len: usize,
    out_len: *mut usize,
    out_cap: *mut usize,
) -> *mut u8 {
    if key_ptr.is_null() || data_ptr.is_null() || out_len.is_null() || out_cap.is_null() {
        return std::ptr::null_mut();
    }
    ffi_encrypt_aes_gcm(data_ptr, data_len, key_ptr, KEY_LEN, out_len, out_cap)
}

/// FFI: Giải mã khối dữ liệu (Chunk) bằng AES-256-GCM tăng tốc phần cứng
#[no_mangle]
pub unsafe extern "C" fn decrypt_chunk_ffi(
    key_ptr: *const u8,
    data_ptr: *const u8,
    data_len: usize,
    out_len: *mut usize,
    out_cap: *mut usize,
) -> *mut u8 {
    if key_ptr.is_null() || data_ptr.is_null() || out_len.is_null() || out_cap.is_null() {
        return std::ptr::null_mut();
    }
    ffi_decrypt_aes_gcm(data_ptr, data_len, key_ptr, KEY_LEN, out_len, out_cap)
}

/// FFI: Tính SHA-256 của chunk dữ liệu trả về C string Hex
#[no_mangle]
pub unsafe extern "C" fn hash_chunk_ffi(data_ptr: *const u8, data_len: usize) -> *mut c_char {
    hash_sha256_hex(data_ptr, data_len)
}

/// FFI Memory Guard (Rule PHAN 7.1): Giải phóng bộ nhớ buffer byte do Rust cấp phát
#[no_mangle]
pub unsafe extern "C" fn free_rust_buffer(ptr: *mut u8, len: usize, cap: usize) {
    free_rust_bytes(ptr, len, cap);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_calculate_sha256() {
        // Test standard known vector for empty string
        assert_eq!(
            calculate_sha256(b""),
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );

        // Test standard known vector for "hello world"
        assert_eq!(
            calculate_sha256(b"hello world"),
            "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
        );

        // Test with custom supportflast message
        let hash = calculate_sha256(b"supportflast.dev.io.vn-ticket-7788");
        assert_eq!(hash.len(), 64);
    }

    #[test]
    fn test_constant_time_comparison() {
        let a = b"supportflast-secure-token-2026";
        let b = b"supportflast-secure-token-2026";
        let c = b"supportflast-invalid-token-XXXX";

        assert!(constant_time_compare(a, b));
        assert!(!constant_time_compare(a, c));
        assert!(!constant_time_compare(a, b"short"));
    }

    #[test]
    fn test_encryption_decryption_cycle() {
        let key = [42u8; KEY_LEN];
        let message = b"Confidential customer ticket for supportflast.dev.io.vn";

        let encrypted = encrypt_support_payload(&key, message).expect("Encryption failed");
        assert_ne!(&encrypted[NONCE_LEN..], message);

        let decrypted = decrypt_support_payload(&key, &encrypted).expect("Decryption failed");
        assert_eq!(decrypted, message);
    }

    #[test]
    fn test_encrypt_decrypt_aes_gcm_api() {
        let key = b"01234567890123456789012345678901"; // 32 bytes
        let plaintext = b"Sensitive customer payload in supportflast";

        // Roundtrip encryption and decryption
        let ciphertext = encrypt_aes_gcm(plaintext, key);
        assert!(!ciphertext.is_empty());
        assert_eq!(ciphertext.len(), NONCE_LEN + plaintext.len() + 16); // 12 + len + 16 (tag)

        let decrypted = decrypt_aes_gcm(&ciphertext, key);
        assert_eq!(decrypted, plaintext);

        // Test invalid key length (not 32 bytes)
        let invalid_key = b"short_key";
        let enc_invalid = encrypt_aes_gcm(plaintext, invalid_key);
        assert!(enc_invalid.is_empty());

        let dec_invalid_key = decrypt_aes_gcm(&ciphertext, invalid_key);
        assert!(dec_invalid_key.is_empty());

        // Test corrupted ciphertext (tampered tag)
        let mut corrupted = ciphertext.clone();
        let last_idx = corrupted.len() - 1;
        corrupted[last_idx] ^= 0xFF;
        let dec_corrupted = decrypt_aes_gcm(&corrupted, key);
        assert!(dec_corrupted.is_empty());

        // Test truncated ciphertext (< NONCE_LEN)
        let truncated = &ciphertext[..5];
        let dec_truncated = decrypt_aes_gcm(truncated, key);
        assert!(dec_truncated.is_empty());
    }

    #[test]
    fn test_ffi_hex_roundtrip_and_memory_safety() {
        let key_bytes = [77u8; KEY_LEN];
        let key_hex = hex::encode(key_bytes);
        let key_c = CString::new(key_hex).unwrap();
        let msg_c = CString::new("FFI encrypted message test").unwrap();

        unsafe {
            let enc_ptr = encrypt_text_hex(key_c.as_ptr(), msg_c.as_ptr());
            assert!(!enc_ptr.is_null());

            let dec_ptr = decrypt_hex_text(key_c.as_ptr(), enc_ptr);
            assert!(!dec_ptr.is_null());
            let dec_str = CStr::from_ptr(dec_ptr).to_str().unwrap();
            assert_eq!(dec_str, "FFI encrypted message test");

            // Test memory cleanup
            free_rust_string(enc_ptr);
            free_rust_string(dec_ptr);
            // Freeing null pointer should be safe and no-op
            free_rust_string(std::ptr::null_mut());
        }
    }

    #[test]
    fn test_ffi_sha256_and_bytes_cleanup() {
        let data = b"FFI data to hash";
        unsafe {
            let hash_ptr = hash_sha256_hex(data.as_ptr(), data.len());
            assert!(!hash_ptr.is_null());
            let hash_str = CStr::from_ptr(hash_ptr).to_str().unwrap();
            assert_eq!(hash_str, calculate_sha256(data));
            free_rust_string(hash_ptr);

            // Test free_rust_bytes with null and valid buffer
            free_rust_bytes(std::ptr::null_mut(), 0, 0);

            let mut out_len: usize = 0;
            let mut out_cap: usize = 0;
            let key = [99u8; KEY_LEN];
            let enc_bytes_ptr = ffi_encrypt_aes_gcm(
                data.as_ptr(),
                data.len(),
                key.as_ptr(),
                key.len(),
                &mut out_len,
                &mut out_cap,
            );
            assert!(!enc_bytes_ptr.is_null());
            assert_eq!(out_len, NONCE_LEN + data.len() + 16);

            let mut dec_len: usize = 0;
            let mut dec_cap: usize = 0;
            let dec_bytes_ptr = ffi_decrypt_aes_gcm(
                enc_bytes_ptr,
                out_len,
                key.as_ptr(),
                key.len(),
                &mut dec_len,
                &mut dec_cap,
            );
            assert!(!dec_bytes_ptr.is_null());
            assert_eq!(dec_len, data.len());
            let dec_slice = std::slice::from_raw_parts(dec_bytes_ptr, dec_len);
            assert_eq!(dec_slice, data);

            free_rust_bytes(enc_bytes_ptr, out_len, out_cap);
            free_rust_bytes(dec_bytes_ptr, dec_len, dec_cap);
        }
    }

    #[test]
    fn test_cloudpool_compatibility_ffi() {
        unsafe {
            let pass = CString::new("test_passphrase_cloudpool").unwrap();
            let mut out_key = [0u8; KEY_LEN];
            let ret = derive_key_ffi(pass.as_ptr(), out_key.as_mut_ptr());
            assert_eq!(ret, 0);
            assert_ne!(out_key, [0u8; KEY_LEN]);

            // Test chunk encrypt & decrypt FFI
            let data = b"CloudPool chunk data to encrypt with supportflast_core";
            let mut enc_len = 0;
            let mut enc_cap = 0;
            let enc_ptr = encrypt_chunk_ffi(
                out_key.as_ptr(),
                data.as_ptr(),
                data.len(),
                &mut enc_len,
                &mut enc_cap,
            );
            assert!(!enc_ptr.is_null());
            assert_eq!(enc_len, NONCE_LEN + data.len() + 16);

            let mut dec_len = 0;
            let mut dec_cap = 0;
            let dec_ptr = decrypt_chunk_ffi(
                out_key.as_ptr(),
                enc_ptr,
                enc_len,
                &mut dec_len,
                &mut dec_cap,
            );
            assert!(!dec_ptr.is_null());
            assert_eq!(dec_len, data.len());
            let dec_slice = std::slice::from_raw_parts(dec_ptr, dec_len);
            assert_eq!(dec_slice, data);

            // Cleanup via free_rust_buffer
            free_rust_buffer(enc_ptr, enc_len, enc_cap);
            free_rust_buffer(dec_ptr, dec_len, dec_cap);

            // Test hash_chunk_ffi
            let hash_ptr = hash_chunk_ffi(data.as_ptr(), data.len());
            assert!(!hash_ptr.is_null());
            let hash_str = CStr::from_ptr(hash_ptr).to_str().unwrap();
            assert_eq!(hash_str, calculate_sha256(data));
            free_rust_string(hash_ptr);
        }
    }
}
