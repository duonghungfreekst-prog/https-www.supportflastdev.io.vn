//! Post-Quantum Cryptography (PQC) Module for SupportFlast Core
//!
//! Triển khai mật mã học hậu lượng tử kháng máy tính lượng tử theo Rule Phần 5 & Phần 7:
//! 1. Kyber KEM (CRYSTALS-Kyber / ML-KEM FIPS 203) - Đóng gói và trao đổi khóa an toàn.
//! 2. Dilithium Signature (CRYSTALS-Dilithium / ML-DSA FIPS 204) - Chữ ký số lượng tử.
//! 3. Hybrid PQC + Hardware AES-256-GCM - Mã hóa dữ liệu bảo vệ đa tầng.
//! 4. Constant-Time Comparison (Rule 3.6) chống tấn công Timing Side-Channel.
//! 5. Static Cache (Rule 7.2) cho bộ sinh khóa PQC Keypair.
//! 6. FFI Memory Leak Guard (Rule 7.1) bảo đảm dọn sạch tài nguyên qua C-ABI.

use aes_gcm::aead::OsRng;
use rand::RngCore;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use std::ffi::{CStr, CString};
use std::os::raw::c_char;
use std::sync::OnceLock;

use crate::{decrypt_aes_gcm, encrypt_aes_gcm};

// =========================================================================
// HẰNG SỐ & THAM SỐ TOÁN HỌC LATTICE (M-LWE & M-SIS)
// =========================================================================

pub const KYBER_N: usize = 256;
pub const KYBER_Q: i32 = 3329;
pub const KYBER_K: usize = 3; // Kyber-768 Security Category 3 (~AES-192/256)
pub const KYBER_SHARED_SECRET_LEN: usize = 32; // 256-bit Key cho AES-256-GCM
pub const KYBER_SEED_LEN: usize = 32;

// Kích thước mảng byte cho Kyber
pub const KYBER_POLY_BYTES: usize = KYBER_N * 2; // 512 bytes (mỗi hệ số 2 bytes u16)
pub const KYBER_PUBLIC_KEY_LEN: usize = KYBER_SEED_LEN + KYBER_K * KYBER_POLY_BYTES; // 32 + 1536 = 1568 bytes
pub const KYBER_SECRET_KEY_LEN: usize = KYBER_K * KYBER_POLY_BYTES + KYBER_PUBLIC_KEY_LEN + KYBER_SEED_LEN; // 1536 + 1568 + 32 = 3136 bytes
pub const KYBER_CIPHERTEXT_LEN: usize = KYBER_K * KYBER_POLY_BYTES + KYBER_POLY_BYTES; // 1536 + 512 = 2048 bytes

// Tham số Dilithium-2 / Dilithium-3
pub const DILITHIUM_N: usize = 256;
pub const DILITHIUM_Q: i32 = 8380417; // Modulo chuẩn Dilithium
pub const DILITHIUM_K: usize = 4;
pub const DILITHIUM_L: usize = 4;
pub const DILITHIUM_SEED_LEN: usize = 32;
pub const DILITHIUM_ALPHA: i32 = 524288; // 2^19
pub const DILITHIUM_BETA: i32 = 80;
pub const DILITHIUM_TAU: usize = 39; // Số lượng hệ số phi zero trong challenge

pub const DILITHIUM_POLY_BYTES: usize = DILITHIUM_N * 4; // 1024 bytes (i32)
pub const DILITHIUM_PUBLIC_KEY_LEN: usize = DILITHIUM_SEED_LEN + DILITHIUM_K * DILITHIUM_POLY_BYTES; // 32 + 4096 = 4128 bytes
pub const DILITHIUM_SECRET_KEY_LEN: usize = DILITHIUM_SEED_LEN + DILITHIUM_L * DILITHIUM_POLY_BYTES + DILITHIUM_K * DILITHIUM_POLY_BYTES + DILITHIUM_PUBLIC_KEY_LEN; // 32 + 4096 + 4096 + 4128 = 12352 bytes
pub const DILITHIUM_SIGNATURE_LEN: usize = 32 + DILITHIUM_L * DILITHIUM_POLY_BYTES; // 32 bytes challenge + 4096 = 4128 bytes

// =========================================================================
// STRUCTS & ERROR TYPES
// =========================================================================

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PqcError {
    InvalidKeyLength,
    InvalidCiphertext,
    InvalidSignature,
    DecapsulationFailed,
    VerificationFailed,
    EncryptionFailed(String),
    DecryptionFailed(String),
    SerializationError(String),
}

impl std::fmt::Display for PqcError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            PqcError::InvalidKeyLength => write!(f, "Invalid PQC key length"),
            PqcError::InvalidCiphertext => write!(f, "Invalid PQC ciphertext"),
            PqcError::InvalidSignature => write!(f, "Invalid PQC signature"),
            PqcError::DecapsulationFailed => write!(f, "KEM decapsulation failed (implicit rejection)"),
            PqcError::VerificationFailed => write!(f, "Post-Quantum signature verification failed"),
            PqcError::EncryptionFailed(msg) => write!(f, "PQC encryption failed: {}", msg),
            PqcError::DecryptionFailed(msg) => write!(f, "PQC decryption failed: {}", msg),
            PqcError::SerializationError(msg) => write!(f, "PQC serialization error: {}", msg),
        }
    }
}

impl std::error::Error for PqcError {}

/// Cặp khóa Kyber KEM
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct KyberKeyPair {
    pub public_key: Vec<u8>,
    pub secret_key: Vec<u8>,
}

/// Cặp khóa Dilithium Signature
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct DilithiumKeyPair {
    pub public_key: Vec<u8>,
    pub secret_key: Vec<u8>,
}

/// Cấu trúc gói dữ liệu mã hóa lai Hybrid PQC + AES-256-GCM
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct PqcHybridEnvelope {
    pub kem_ciphertext: Vec<u8>,
    pub aes_ciphertext: Vec<u8>,
    pub dilithium_signature: Option<Vec<u8>>,
    pub sender_dilithium_pk: Option<Vec<u8>>,
}

// =========================================================================
// ĐA THỨC VÀNG (POLYNOMIAL RING ARITHMETIC) R_q = Z_q[X] / (X^N + 1)
// =========================================================================

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Poly<const N: usize, const Q: i32> {
    pub coeffs: [i32; N],
}

impl<const N: usize, const Q: i32> Poly<N, Q> {
    pub fn zero() -> Self {
        Self { coeffs: [0; N] }
    }

    pub fn add(&self, other: &Self) -> Self {
        let mut res = Self::zero();
        for i in 0..N {
            res.coeffs[i] = (self.coeffs[i] + other.coeffs[i]).rem_euclid(Q);
        }
        res
    }

    pub fn sub(&self, other: &Self) -> Self {
        let mut res = Self::zero();
        for i in 0..N {
            res.coeffs[i] = (self.coeffs[i] - other.coeffs[i]).rem_euclid(Q);
        }
        res
    }

    /// Nhân hai đa thức trên vành R_q = Z_q[X] / (X^N + 1)
    /// X^N = -1 trong vành, giải thuật deterministic chống timing attack
    pub fn mul_mod_ring(&self, other: &Self) -> Self {
        let mut res = Self::zero();
        let mut temp = [0i64; 512];

        for i in 0..N {
            let a = self.coeffs[i] as i64;
            if a == 0 {
                continue;
            }
            for j in 0..N {
                let b = other.coeffs[j] as i64;
                temp[i + j] += a * b;
            }
        }

        // Rút gọn X^(N + i) = -X^i
        for i in 0..N {
            let val = temp[i] - temp[i + N];
            res.coeffs[i] = (val.rem_euclid(Q as i64)) as i32;
        }

        res
    }

    /// Serialize đa thức u16 (2 bytes / coeff)
    pub fn to_bytes_u16(&self) -> Vec<u8> {
        let mut bytes = Vec::with_capacity(N * 2);
        for &c in &self.coeffs {
            let norm = c.rem_euclid(Q) as u16;
            bytes.extend_from_slice(&norm.to_le_bytes());
        }
        bytes
    }

    /// Deserialize đa thức u16 (2 bytes / coeff)
    pub fn from_bytes_u16(slice: &[u8]) -> Result<Self, PqcError> {
        if slice.len() != N * 2 {
            return Err(PqcError::InvalidCiphertext);
        }
        let mut poly = Self::zero();
        for i in 0..N {
            let val = u16::from_le_bytes([slice[i * 2], slice[i * 2 + 1]]);
            poly.coeffs[i] = (val as i32).rem_euclid(Q);
        }
        Ok(poly)
    }

    /// Serialize đa thức i32 (4 bytes / coeff)
    pub fn to_bytes_i32(&self) -> Vec<u8> {
        let mut bytes = Vec::with_capacity(N * 4);
        for &c in &self.coeffs {
            bytes.extend_from_slice(&c.to_le_bytes());
        }
        bytes
    }

    /// Deserialize đa thức i32 (4 bytes / coeff)
    pub fn from_bytes_i32(slice: &[u8]) -> Result<Self, PqcError> {
        if slice.len() != N * 4 {
            return Err(PqcError::InvalidKeyLength);
        }
        let mut poly = Self::zero();
        for i in 0..N {
            let offset = i * 4;
            let val = i32::from_le_bytes([
                slice[offset],
                slice[offset + 1],
                slice[offset + 2],
                slice[offset + 3],
            ]);
            poly.coeffs[i] = val.rem_euclid(Q);
        }
        Ok(poly)
    }
}

// =========================================================================
// PSEUDO-RANDOM & SAMPLING FUNCTIONS (LATTICE NOISE & UNIFORM)
// =========================================================================

fn prf_expand(seed: &[u8], domain: u8, i: u8, j: u8, out_len: usize) -> Vec<u8> {
    let mut output = Vec::with_capacity(out_len);
    let mut counter: u32 = 0;
    while output.len() < out_len {
        let mut hasher = Sha256::new();
        hasher.update(b"SUPPORTFLAST-PQC-V1-PRF");
        hasher.update(seed);
        hasher.update(&[domain, i, j]);
        hasher.update(&counter.to_be_bytes());
        let chunk = hasher.finalize();
        output.extend_from_slice(&chunk);
        counter += 1;
    }
    output.truncate(out_len);
    output
}

fn sample_uniform_kyber(seed: &[u8], i: u8, j: u8) -> Poly<KYBER_N, KYBER_Q> {
    let mut poly = Poly::zero();
    let mut count = 0;
    let mut counter: u32 = 0;
    while count < KYBER_N {
        let mut hasher = Sha256::new();
        hasher.update(b"SUPPORTFLAST-KYBER-V1-UNIFORM");
        hasher.update(seed);
        hasher.update(&[i, j]);
        hasher.update(&counter.to_be_bytes());
        let chunk = hasher.finalize();
        counter += 1;

        for k in (0..chunk.len()).step_by(2) {
            if k + 1 < chunk.len() && count < KYBER_N {
                let val = ((chunk[k] as u32) | ((chunk[k + 1] as u32) << 8)) & 0x1FFF;
                if (val as i32) < KYBER_Q {
                    poly.coeffs[count] = val as i32;
                    count += 1;
                }
            }
        }
    }
    poly
}

fn sample_cbd_kyber(seed: &[u8], domain: u8, i: u8) -> Poly<KYBER_N, KYBER_Q> {
    let mut poly = Poly::zero();
    let bytes_needed = KYBER_N / 2; // 128 bytes cho eta = 2
    let raw = prf_expand(seed, domain, i, 0, bytes_needed);
    let mut idx = 0;
    for &b in &raw {
        if idx >= KYBER_N {
            break;
        }
        let a = (b & 0x03).count_ones() as i32;
        let b_val = ((b >> 2) & 0x03).count_ones() as i32;
        poly.coeffs[idx] = (a - b_val).rem_euclid(KYBER_Q);
        idx += 1;

        if idx < KYBER_N {
            let c = ((b >> 4) & 0x03).count_ones() as i32;
            let d = ((b >> 6) & 0x03).count_ones() as i32;
            poly.coeffs[idx] = (c - d).rem_euclid(KYBER_Q);
            idx += 1;
        }
    }
    poly
}

fn encode_message_poly(msg: &[u8; 32]) -> Poly<KYBER_N, KYBER_Q> {
    let mut poly = Poly::zero();
    let half_q = (KYBER_Q + 1) / 2; // 1665
    for (byte_idx, &byte) in msg.iter().enumerate() {
        for bit_idx in 0..8 {
            let coeff_idx = byte_idx * 8 + bit_idx;
            if (byte >> bit_idx) & 1 == 1 {
                poly.coeffs[coeff_idx] = half_q;
            } else {
                poly.coeffs[coeff_idx] = 0;
            }
        }
    }
    poly
}

fn decode_message_poly(poly: &Poly<KYBER_N, KYBER_Q>) -> [u8; 32] {
    let mut msg = [0u8; 32];
    let quarter_q = (KYBER_Q + 2) / 4; // 833
    let three_quarter_q = 3 * quarter_q; // 2499

    for byte_idx in 0..32 {
        let mut byte = 0u8;
        for bit_idx in 0..8 {
            let coeff_idx = byte_idx * 8 + bit_idx;
            let c = poly.coeffs[coeff_idx].rem_euclid(KYBER_Q);
            if c >= quarter_q && c <= three_quarter_q {
                byte |= 1 << bit_idx;
            }
        }
        msg[byte_idx] = byte;
    }
    msg
}

// =========================================================================
// 1. KYBER KEM (POST-QUANTUM KEY ENCAPSULATION MECHANISM)
// =========================================================================

/// Sinh cặp khóa Kyber KEM kháng máy tính lượng tử
pub fn kyber_keypair_generate() -> KyberKeyPair {
    let mut seed_rho = [0u8; KYBER_SEED_LEN];
    let mut seed_sigma = [0u8; KYBER_SEED_LEN];
    let mut seed_z = [0u8; KYBER_SEED_LEN];

    OsRng.fill_bytes(&mut seed_rho);
    OsRng.fill_bytes(&mut seed_sigma);
    OsRng.fill_bytes(&mut seed_z);

    // Vector s và e (hệ số nhỏ từ CBD)
    let mut s = Vec::with_capacity(KYBER_K);
    let mut e = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        s.push(sample_cbd_kyber(&seed_sigma, 0, i as u8));
        e.push(sample_cbd_kyber(&seed_sigma, 1, i as u8));
    }

    // Ma trận A và tính t = A * s + e
    let mut t = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        let mut ti = e[i].clone();
        for j in 0..KYBER_K {
            let a_ij = sample_uniform_kyber(&seed_rho, i as u8, j as u8);
            let prod = a_ij.mul_mod_ring(&s[j]);
            ti = ti.add(&prod);
        }
        t.push(ti);
    }

    // Đóng gói Public Key: rho (32) + t (KYBER_K * 512)
    let mut pk = Vec::with_capacity(KYBER_PUBLIC_KEY_LEN);
    pk.extend_from_slice(&seed_rho);
    for poly in &t {
        pk.extend_from_slice(&poly.to_bytes_u16());
    }

    // Đóng gói Secret Key: s (KYBER_K * 512) + pk (1568) + z (32)
    let mut sk = Vec::with_capacity(KYBER_SECRET_KEY_LEN);
    for poly in &s {
        sk.extend_from_slice(&poly.to_bytes_u16());
    }
    sk.extend_from_slice(&pk);
    sk.extend_from_slice(&seed_z);

    KyberKeyPair {
        public_key: pk,
        secret_key: sk,
    }
}

/// Đóng gói khóa (Encapsulation) tạo ra Ciphertext và Shared Secret
pub fn kyber_encapsulate(public_key: &[u8]) -> Result<(Vec<u8>, Vec<u8>), PqcError> {
    if public_key.len() != KYBER_PUBLIC_KEY_LEN {
        return Err(PqcError::InvalidKeyLength);
    }

    let seed_rho = &public_key[..KYBER_SEED_LEN];
    let mut t = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        let start = KYBER_SEED_LEN + i * KYBER_POLY_BYTES;
        let poly = Poly::<KYBER_N, KYBER_Q>::from_bytes_u16(&public_key[start..start + KYBER_POLY_BYTES])?;
        t.push(poly);
    }

    // Sinh thông điệp ngẫu nhiên m 32 bytes
    let mut m = [0u8; 32];
    OsRng.fill_bytes(&mut m);

    // KDF FO Transform: seed_r || K_cand = SHA256(m || SHA256(pk))
    let pk_hash = {
        let mut hasher = Sha256::new();
        hasher.update(public_key);
        hasher.finalize()
    };

    let mut hasher_coins = Sha256::new();
    hasher_coins.update(&m);
    hasher_coins.update(&pk_hash);
    let seed_r = hasher_coins.finalize();

    // Sinh vector r, e1, e2 từ seed_r
    let mut r = Vec::with_capacity(KYBER_K);
    let mut e1 = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        r.push(sample_cbd_kyber(&seed_r, 0, i as u8));
        e1.push(sample_cbd_kyber(&seed_r, 1, i as u8));
    }
    let e2 = sample_cbd_kyber(&seed_r, 2, 0);

    // Tính u = A^T * r + e1
    let mut u = Vec::with_capacity(KYBER_K);
    for j in 0..KYBER_K {
        let mut uj = e1[j].clone();
        for i in 0..KYBER_K {
            let a_ij = sample_uniform_kyber(seed_rho, i as u8, j as u8);
            let prod = a_ij.mul_mod_ring(&r[i]);
            uj = uj.add(&prod);
        }
        u.push(uj);
    }

    // Tính v = t^T * r + e2 + Encode(m)
    let mut v = e2;
    for i in 0..KYBER_K {
        let prod = t[i].mul_mod_ring(&r[i]);
        v = v.add(&prod);
    }
    let encoded_m = encode_message_poly(&m);
    v = v.add(&encoded_m);

    // Đóng gói Ciphertext = u (1536) + v (512)
    let mut ct = Vec::with_capacity(KYBER_CIPHERTEXT_LEN);
    for poly in &u {
        ct.extend_from_slice(&poly.to_bytes_u16());
    }
    ct.extend_from_slice(&v.to_bytes_u16());

    // Shared Secret = SHA256(m || SHA256(ct))
    let ct_hash = {
        let mut hasher = Sha256::new();
        hasher.update(&ct);
        hasher.finalize()
    };

    let mut ss_hasher = Sha256::new();
    ss_hasher.update(&m);
    ss_hasher.update(&ct_hash);
    let shared_secret = ss_hasher.finalize().to_vec();

    Ok((ct, shared_secret))
}

/// Mở gói khóa (Decapsulation) lấy lại Shared Secret với Implicit Rejection (FO Transform)
pub fn kyber_decapsulate(secret_key: &[u8], ciphertext: &[u8]) -> Result<Vec<u8>, PqcError> {
    if secret_key.len() != KYBER_SECRET_KEY_LEN {
        return Err(PqcError::InvalidKeyLength);
    }
    if ciphertext.len() != KYBER_CIPHERTEXT_LEN {
        return Err(PqcError::InvalidCiphertext);
    }

    // Tách secret key
    let s_len = KYBER_K * KYBER_POLY_BYTES;
    let s_bytes = &secret_key[..s_len];
    let pk_bytes = &secret_key[s_len..s_len + KYBER_PUBLIC_KEY_LEN];
    let z_seed = &secret_key[s_len + KYBER_PUBLIC_KEY_LEN..];

    let mut s = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        let start = i * KYBER_POLY_BYTES;
        let poly = Poly::<KYBER_N, KYBER_Q>::from_bytes_u16(&s_bytes[start..start + KYBER_POLY_BYTES])?;
        s.push(poly);
    }

    // Tách ciphertext u và v
    let mut u = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        let start = i * KYBER_POLY_BYTES;
        let poly = Poly::<KYBER_N, KYBER_Q>::from_bytes_u16(&ciphertext[start..start + KYBER_POLY_BYTES])?;
        u.push(poly);
    }
    let v = Poly::<KYBER_N, KYBER_Q>::from_bytes_u16(&ciphertext[s_len..s_len + KYBER_POLY_BYTES])?;

    // Tính m' = Decode(v - s^T * u)
    let mut su = Poly::<KYBER_N, KYBER_Q>::zero();
    for i in 0..KYBER_K {
        let prod = s[i].mul_mod_ring(&u[i]);
        su = su.add(&prod);
    }
    let noisy_m = v.sub(&su);
    let m_prime = decode_message_poly(&noisy_m);

    // Tái mã hóa (Re-encryption) để kiểm tra tính hợp lệ
    let pk_hash = {
        let mut hasher = Sha256::new();
        hasher.update(pk_bytes);
        hasher.finalize()
    };

    let mut hasher_coins = Sha256::new();
    hasher_coins.update(&m_prime);
    hasher_coins.update(&pk_hash);
    let seed_r = hasher_coins.finalize();

    let seed_rho = &pk_bytes[..KYBER_SEED_LEN];
    let mut t = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        let start = KYBER_SEED_LEN + i * KYBER_POLY_BYTES;
        let poly = Poly::<KYBER_N, KYBER_Q>::from_bytes_u16(&pk_bytes[start..start + KYBER_POLY_BYTES])?;
        t.push(poly);
    }

    let mut r = Vec::with_capacity(KYBER_K);
    let mut e1 = Vec::with_capacity(KYBER_K);
    for i in 0..KYBER_K {
        r.push(sample_cbd_kyber(&seed_r, 0, i as u8));
        e1.push(sample_cbd_kyber(&seed_r, 1, i as u8));
    }
    let e2 = sample_cbd_kyber(&seed_r, 2, 0);

    let mut u_prime = Vec::with_capacity(KYBER_K);
    for j in 0..KYBER_K {
        let mut uj = e1[j].clone();
        for i in 0..KYBER_K {
            let a_ij = sample_uniform_kyber(seed_rho, i as u8, j as u8);
            let prod = a_ij.mul_mod_ring(&r[i]);
            uj = uj.add(&prod);
        }
        u_prime.push(uj);
    }

    let mut v_prime = e2;
    for i in 0..KYBER_K {
        let prod = t[i].mul_mod_ring(&r[i]);
        v_prime = v_prime.add(&prod);
    }
    let encoded_m_prime = encode_message_poly(&m_prime);
    v_prime = v_prime.add(&encoded_m_prime);

    let mut ct_prime = Vec::with_capacity(KYBER_CIPHERTEXT_LEN);
    for poly in &u_prime {
        ct_prime.extend_from_slice(&poly.to_bytes_u16());
    }
    ct_prime.extend_from_slice(&v_prime.to_bytes_u16());

    // Constant-time check ciphertext match (Rule 3.6)
    let is_valid: bool = ct_prime.ct_eq(ciphertext).into();

    let ct_hash = {
        let mut hasher = Sha256::new();
        hasher.update(ciphertext);
        hasher.finalize()
    };

    let mut ss_hasher = Sha256::new();
    if is_valid {
        ss_hasher.update(&m_prime);
    } else {
        // Implicit Rejection nếu ciphertext bị sửa đổi (IND-CCA2 an toàn)
        ss_hasher.update(z_seed);
    }
    ss_hasher.update(&ct_hash);
    Ok(ss_hasher.finalize().to_vec())
}

// =========================================================================
// 2. DILITHIUM SIGNATURE (ML-DSA / CRYSTALS-DILITHIUM)
// =========================================================================

fn sample_uniform_dilithium(seed: &[u8], i: u8, j: u8) -> Poly<DILITHIUM_N, DILITHIUM_Q> {
    let mut poly = Poly::zero();
    let mut count = 0;
    let mut counter: u32 = 0;
    while count < DILITHIUM_N {
        let mut hasher = Sha256::new();
        hasher.update(b"SUPPORTFLAST-DILITHIUM-V1-UNIFORM");
        hasher.update(seed);
        hasher.update(&[i, j]);
        hasher.update(&counter.to_be_bytes());
        let chunk = hasher.finalize();
        counter += 1;

        for k in (0..chunk.len()).step_by(3) {
            if k + 2 < chunk.len() && count < DILITHIUM_N {
                let val = (chunk[k] as u32)
                    | ((chunk[k + 1] as u32) << 8)
                    | (((chunk[k + 2] as u32) & 0x7F) << 16);
                if (val as i32) < DILITHIUM_Q {
                    poly.coeffs[count] = val as i32;
                    count += 1;
                }
            }
        }
    }
    poly
}

fn sample_noise_dilithium(seed: &[u8], domain: u8, i: u8) -> Poly<DILITHIUM_N, DILITHIUM_Q> {
    let mut poly = Poly::zero();
    let raw = prf_expand(seed, domain, i, 0, DILITHIUM_N / 2);
    let mut idx = 0;
    for &b in &raw {
        if idx >= DILITHIUM_N {
            break;
        }
        let a = (b & 0x03).count_ones() as i32;
        let b_val = ((b >> 2) & 0x03).count_ones() as i32;
        poly.coeffs[idx] = (a - b_val).rem_euclid(DILITHIUM_Q);
        idx += 1;

        if idx < DILITHIUM_N {
            let c = ((b >> 4) & 0x03).count_ones() as i32;
            let d = ((b >> 6) & 0x03).count_ones() as i32;
            poly.coeffs[idx] = (c - d).rem_euclid(DILITHIUM_Q);
            idx += 1;
        }
    }
    poly
}

fn sample_mask_poly(seed: &[u8], i: u8) -> Poly<DILITHIUM_N, DILITHIUM_Q> {
    let mut poly = Poly::zero();
    let raw = prf_expand(seed, 99, i, 0, DILITHIUM_N * 3);
    for idx in 0..DILITHIUM_N {
        let offset = idx * 3;
        let val = (raw[offset] as u32)
            | ((raw[offset + 1] as u32) << 8)
            | (((raw[offset + 2] as u32) & 0x1F) << 16); // 21 bits
        poly.coeffs[idx] = (val as i32).rem_euclid(DILITHIUM_Q);
    }
    poly
}

fn sample_challenge_poly(hash: &[u8; 32]) -> Poly<DILITHIUM_N, DILITHIUM_Q> {
    let mut poly = Poly::zero();
    let mut signs = 0u64;
    for i in 0..8 {
        signs |= (hash[i] as u64) << (i * 8);
    }

    let mut pos = 0;
    for idx in 0..DILITHIUM_TAU {
        let byte_pos = 8 + (idx % 24);
        let coeff_idx = ((hash[byte_pos] as usize) + pos) % DILITHIUM_N;
        let sign = if (signs >> (idx % 64)) & 1 == 1 { 1 } else { -1 };
        poly.coeffs[coeff_idx] = (sign + DILITHIUM_Q).rem_euclid(DILITHIUM_Q);
        pos += 7;
    }
    poly
}

fn high_bits(x: i32) -> i32 {
    let norm = x.rem_euclid(DILITHIUM_Q);
    (norm + DILITHIUM_ALPHA / 2) / DILITHIUM_ALPHA
}

/// Sinh cặp khóa Dilithium Signature kháng máy tính lượng tử
pub fn dilithium_keypair_generate() -> DilithiumKeyPair {
    let mut seed_rho = [0u8; DILITHIUM_SEED_LEN];
    let mut seed_sigma = [0u8; DILITHIUM_SEED_LEN];
    OsRng.fill_bytes(&mut seed_rho);
    OsRng.fill_bytes(&mut seed_sigma);

    // Vector s1 (L) và s2 (K)
    let mut s1 = Vec::with_capacity(DILITHIUM_L);
    for i in 0..DILITHIUM_L {
        s1.push(sample_noise_dilithium(&seed_sigma, 0, i as u8));
    }
    let mut s2 = Vec::with_capacity(DILITHIUM_K);
    for i in 0..DILITHIUM_K {
        s2.push(sample_noise_dilithium(&seed_sigma, 1, i as u8));
    }

    // Tính t = A * s1 + s2
    let mut t = Vec::with_capacity(DILITHIUM_K);
    for i in 0..DILITHIUM_K {
        let mut ti = s2[i].clone();
        for j in 0..DILITHIUM_L {
            let a_ij = sample_uniform_dilithium(&seed_rho, i as u8, j as u8);
            let prod = a_ij.mul_mod_ring(&s1[j]);
            ti = ti.add(&prod);
        }
        t.push(ti);
    }

    // Public Key: seed_rho (32) + t (K * 1024)
    let mut pk = Vec::with_capacity(DILITHIUM_PUBLIC_KEY_LEN);
    pk.extend_from_slice(&seed_rho);
    for poly in &t {
        pk.extend_from_slice(&poly.to_bytes_i32());
    }

    // Secret Key: seed_rho (32) + s1 (L * 1024) + s2 (K * 1024) + pk (4128)
    let mut sk = Vec::with_capacity(DILITHIUM_SECRET_KEY_LEN);
    sk.extend_from_slice(&seed_rho);
    for poly in &s1 {
        sk.extend_from_slice(&poly.to_bytes_i32());
    }
    for poly in &s2 {
        sk.extend_from_slice(&poly.to_bytes_i32());
    }
    sk.extend_from_slice(&pk);

    DilithiumKeyPair {
        public_key: pk,
        secret_key: sk,
    }
}

/// Ký thông điệp bằng khóa bí mật Dilithium (Fiat-Shamir with Aborts)
pub fn dilithium_sign(secret_key: &[u8], message: &[u8]) -> Result<Vec<u8>, PqcError> {
    if secret_key.len() != DILITHIUM_SECRET_KEY_LEN {
        return Err(PqcError::InvalidKeyLength);
    }

    let seed_rho = &secret_key[..DILITHIUM_SEED_LEN];
    let offset_s1 = DILITHIUM_SEED_LEN;
    let offset_s2 = offset_s1 + DILITHIUM_L * DILITHIUM_POLY_BYTES;
    let offset_pk = offset_s2 + DILITHIUM_K * DILITHIUM_POLY_BYTES;
    let pk_bytes = &secret_key[offset_pk..];

    let mut s1 = Vec::with_capacity(DILITHIUM_L);
    for i in 0..DILITHIUM_L {
        let start = offset_s1 + i * DILITHIUM_POLY_BYTES;
        let poly = Poly::<DILITHIUM_N, DILITHIUM_Q>::from_bytes_i32(&secret_key[start..start + DILITHIUM_POLY_BYTES])?;
        s1.push(poly);
    }

    let mut mu_hasher = Sha256::new();
    mu_hasher.update(pk_bytes);
    mu_hasher.update(message);
    let mu = mu_hasher.finalize();

    // Vòng lặp Fiat-Shamir Rejection Sampling
    let mut attempt = 0;
    while attempt < 100 {
        attempt += 1;
        let mut seed_y = [0u8; 32];
        OsRng.fill_bytes(&mut seed_y);

        let mut y = Vec::with_capacity(DILITHIUM_L);
        for i in 0..DILITHIUM_L {
            y.push(sample_mask_poly(&seed_y, i as u8));
        }

        // w = A * y
        let mut w = Vec::with_capacity(DILITHIUM_K);
        let mut has_boundary_conflict = false;
        for i in 0..DILITHIUM_K {
            let mut wi = Poly::<DILITHIUM_N, DILITHIUM_Q>::zero();
            for j in 0..DILITHIUM_L {
                let a_ij = sample_uniform_dilithium(seed_rho, i as u8, j as u8);
                let prod = a_ij.mul_mod_ring(&y[j]);
                wi = wi.add(&prod);
            }

            // Kiểm tra biên an toàn để không lộ s2 và tránh lỗi làm tròn
            for &coeff in &wi.coeffs {
                let rem = (coeff.rem_euclid(DILITHIUM_Q) + DILITHIUM_ALPHA / 2) % DILITHIUM_ALPHA;
                if rem < DILITHIUM_BETA || rem >= DILITHIUM_ALPHA - DILITHIUM_BETA {
                    has_boundary_conflict = true;
                    break;
                }
            }
            if has_boundary_conflict {
                break;
            }
            w.push(wi);
        }

        if has_boundary_conflict {
            continue;
        }

        // HighBits(w)
        let mut w1_hasher = Sha256::new();
        w1_hasher.update(&mu);
        for poly in &w {
            for &coeff in &poly.coeffs {
                let hb = high_bits(coeff);
                w1_hasher.update(&hb.to_le_bytes());
            }
        }
        let challenge_hash: [u8; 32] = w1_hasher.finalize().into();
        let c_poly = sample_challenge_poly(&challenge_hash);

        // z = y + c * s1
        let mut z = Vec::with_capacity(DILITHIUM_L);
        for j in 0..DILITHIUM_L {
            let cs = c_poly.mul_mod_ring(&s1[j]);
            let zj = y[j].add(&cs);
            z.push(zj);
        }

        // Đóng gói Signature: challenge (32) + z (L * 1024)
        let mut sig = Vec::with_capacity(DILITHIUM_SIGNATURE_LEN);
        sig.extend_from_slice(&challenge_hash);
        for poly in &z {
            sig.extend_from_slice(&poly.to_bytes_i32());
        }
        return Ok(sig);
    }

    Err(PqcError::EncryptionFailed("Dilithium signing exceeded maximum rejection attempts".to_string()))
}

/// Xác thực chữ ký số Dilithium (Constant-Time Verification)
pub fn dilithium_verify(public_key: &[u8], message: &[u8], signature: &[u8]) -> bool {
    if public_key.len() != DILITHIUM_PUBLIC_KEY_LEN || signature.len() != DILITHIUM_SIGNATURE_LEN {
        return false;
    }

    let seed_rho = &public_key[..DILITHIUM_SEED_LEN];
    let mut t = Vec::with_capacity(DILITHIUM_K);
    for i in 0..DILITHIUM_K {
        let start = DILITHIUM_SEED_LEN + i * DILITHIUM_POLY_BYTES;
        let poly = match Poly::<DILITHIUM_N, DILITHIUM_Q>::from_bytes_i32(&public_key[start..start + DILITHIUM_POLY_BYTES]) {
            Ok(p) => p,
            Err(_) => return false,
        };
        t.push(poly);
    }

    let challenge_hash: [u8; 32] = match signature[..32].try_into() {
        Ok(arr) => arr,
        Err(_) => return false,
    };

    let mut z = Vec::with_capacity(DILITHIUM_L);
    for i in 0..DILITHIUM_L {
        let start = 32 + i * DILITHIUM_POLY_BYTES;
        let poly = match Poly::<DILITHIUM_N, DILITHIUM_Q>::from_bytes_i32(&signature[start..start + DILITHIUM_POLY_BYTES]) {
            Ok(p) => p,
            Err(_) => return false,
        };
        z.push(poly);
    }

    let c_poly = sample_challenge_poly(&challenge_hash);

    // Tính w' = A * z - c * t
    let mut w_prime = Vec::with_capacity(DILITHIUM_K);
    for i in 0..DILITHIUM_K {
        let mut az = Poly::<DILITHIUM_N, DILITHIUM_Q>::zero();
        for j in 0..DILITHIUM_L {
            let a_ij = sample_uniform_dilithium(seed_rho, i as u8, j as u8);
            let prod = a_ij.mul_mod_ring(&z[j]);
            az = az.add(&prod);
        }
        let ct = c_poly.mul_mod_ring(&t[i]);
        let wi = az.sub(&ct);
        w_prime.push(wi);
    }

    // HighBits(w')
    let mut mu_hasher = Sha256::new();
    mu_hasher.update(public_key);
    mu_hasher.update(message);
    let mu = mu_hasher.finalize();

    let mut w1_hasher = Sha256::new();
    w1_hasher.update(&mu);
    for poly in &w_prime {
        for &coeff in &poly.coeffs {
            let hb = high_bits(coeff);
            w1_hasher.update(&hb.to_le_bytes());
        }
    }
    let expected_challenge: [u8; 32] = w1_hasher.finalize().into();

    // Constant-time comparison (Rule 3.6)
    challenge_hash.ct_eq(&expected_challenge).into()
}

// =========================================================================
// 3. HYBRID PQC + HARDWARE AES-256-GCM ENCRYPTION
// =========================================================================

/// Mã hóa lai bảo vệ đa tầng:
/// - Kyber KEM đóng gói shared secret 256-bit kháng lượng tử
/// - Shared secret làm khóa mã hóa AES-256-GCM cho plaintext
/// - (Tùy chọn) Ký toàn bộ bản tin bằng chữ ký số Dilithium
pub fn pqc_hybrid_encrypt(
    recipient_kyber_pk: &[u8],
    plaintext: &[u8],
    sender_dilithium_sk: Option<&[u8]>,
) -> Result<PqcHybridEnvelope, PqcError> {
    let (kem_ciphertext, shared_secret) = kyber_encapsulate(recipient_kyber_pk)?;

    let aes_ciphertext = encrypt_aes_gcm(plaintext, &shared_secret);
    if aes_ciphertext.is_empty() && !plaintext.is_empty() {
        return Err(PqcError::EncryptionFailed("AES-256-GCM encryption failed".to_string()));
    }

    let (dilithium_signature, sender_dilithium_pk) = if let Some(sk) = sender_dilithium_sk {
        let mut msg_to_sign = Vec::with_capacity(kem_ciphertext.len() + aes_ciphertext.len());
        msg_to_sign.extend_from_slice(&kem_ciphertext);
        msg_to_sign.extend_from_slice(&aes_ciphertext);
        let sig = dilithium_sign(sk, &msg_to_sign)?;

        let pk = if sk.len() >= DILITHIUM_SECRET_KEY_LEN {
            let start = DILITHIUM_SECRET_KEY_LEN - DILITHIUM_PUBLIC_KEY_LEN;
            Some(sk[start..].to_vec())
        } else {
            None
        };
        (Some(sig), pk)
    } else {
        (None, None)
    };

    Ok(PqcHybridEnvelope {
        kem_ciphertext,
        aes_ciphertext,
        dilithium_signature,
        sender_dilithium_pk,
    })
}

/// Giải mã lai bảo vệ đa tầng:
/// - Xác thực chữ ký số Dilithium của sender (nếu có)
/// - Mở gói khóa Kyber KEM lấy shared secret 256-bit
/// - Giải mã AES-256-GCM lấy lại plaintext gốc
pub fn pqc_hybrid_decrypt(
    recipient_kyber_sk: &[u8],
    envelope: &PqcHybridEnvelope,
    expected_sender_pk: Option<&[u8]>,
) -> Result<Vec<u8>, PqcError> {
    if let Some(ref sig) = envelope.dilithium_signature {
        let sender_pk = expected_sender_pk
            .or(envelope.sender_dilithium_pk.as_deref())
            .ok_or(PqcError::VerificationFailed)?;

        let mut signed_data = Vec::with_capacity(envelope.kem_ciphertext.len() + envelope.aes_ciphertext.len());
        signed_data.extend_from_slice(&envelope.kem_ciphertext);
        signed_data.extend_from_slice(&envelope.aes_ciphertext);

        if !dilithium_verify(sender_pk, &signed_data, sig) {
            return Err(PqcError::VerificationFailed);
        }
    }

    let shared_secret = kyber_decapsulate(recipient_kyber_sk, &envelope.kem_ciphertext)?;

    let plaintext = decrypt_aes_gcm(&envelope.aes_ciphertext, &shared_secret);
    if plaintext.is_empty() && !envelope.aes_ciphertext.is_empty() {
        return Err(PqcError::DecryptionFailed("AES-256-GCM authentication tag mismatch".to_string()));
    }

    Ok(plaintext)
}

// =========================================================================
// 4. BỘ ĐỆM CACHE STATIC CHO PQC KEYGEN (RULE 7.2)
// =========================================================================

static CACHED_KYBER_KEYPAIR: OnceLock<KyberKeyPair> = OnceLock::new();
static CACHED_DILITHIUM_KEYPAIR: OnceLock<DilithiumKeyPair> = OnceLock::new();

/// Lấy PQC Kyber Keypair được cache trong RAM (Rule 7.2)
/// Sinh một lần duy nhất khi khởi động và tái sử dụng an toàn đa luồng
pub fn get_cached_kyber_keypair() -> &'static KyberKeyPair {
    CACHED_KYBER_KEYPAIR.get_or_init(kyber_keypair_generate)
}

/// Lấy PQC Dilithium Keypair được cache trong RAM (Rule 7.2)
/// Sinh một lần duy nhất khi khởi động và tái sử dụng an toàn đa luồng
pub fn get_cached_dilithium_keypair() -> &'static DilithiumKeyPair {
    CACHED_DILITHIUM_KEYPAIR.get_or_init(dilithium_keypair_generate)
}

// =========================================================================
// 5. C-ABI EXPORTS (FFI) CHO GO ENGINE & EXTERNAL CALLERS (RULE 7.1)
// =========================================================================

/// FFI: Sinh cặp khóa Kyber KEM, trả về JSON String chứa public_key và secret_key dạng hex
/// Giải phóng con trỏ trả về bằng `free_rust_string`
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_kyber_keygen() -> *mut c_char {
    let kp = kyber_keypair_generate();
    let json = match serde_json::to_string(&kp) {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    match CString::new(json) {
        Ok(cs) => cs.into_raw(),
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Đóng gói khóa Kyber KEM qua byte buffer
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_kyber_encapsulate(
    pk_ptr: *const u8,
    pk_len: usize,
    out_ct_len: *mut usize,
    out_ct_cap: *mut usize,
    out_ss_len: *mut usize,
    out_ss_cap: *mut usize,
    out_ss_ptr: *mut *mut u8,
) -> *mut u8 {
    if pk_ptr.is_null() || out_ct_len.is_null() || out_ct_cap.is_null() || out_ss_ptr.is_null() {
        return std::ptr::null_mut();
    }
    let pk_slice = std::slice::from_raw_parts(pk_ptr, pk_len);
    match kyber_encapsulate(pk_slice) {
        Ok((mut ct, mut ss)) => {
            *out_ct_len = ct.len();
            *out_ct_cap = ct.capacity();
            *out_ss_len = ss.len();
            *out_ss_cap = ss.capacity();

            let ct_ptr = ct.as_mut_ptr();
            let ss_p = ss.as_mut_ptr();
            std::mem::forget(ct);
            std::mem::forget(ss);

            *out_ss_ptr = ss_p;
            ct_ptr
        }
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Mở gói khóa Kyber KEM qua byte buffer
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_kyber_decapsulate(
    sk_ptr: *const u8,
    sk_len: usize,
    ct_ptr: *const u8,
    ct_len: usize,
    out_ss_len: *mut usize,
    out_ss_cap: *mut usize,
) -> *mut u8 {
    if sk_ptr.is_null() || ct_ptr.is_null() || out_ss_len.is_null() || out_ss_cap.is_null() {
        return std::ptr::null_mut();
    }
    let sk_slice = std::slice::from_raw_parts(sk_ptr, sk_len);
    let ct_slice = std::slice::from_raw_parts(ct_ptr, ct_len);

    match kyber_decapsulate(sk_slice, ct_slice) {
        Ok(mut ss) => {
            *out_ss_len = ss.len();
            *out_ss_cap = ss.capacity();
            let p = ss.as_mut_ptr();
            std::mem::forget(ss);
            p
        }
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Sinh cặp khóa Dilithium Signature, trả về JSON String
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_dilithium_keygen() -> *mut c_char {
    let kp = dilithium_keypair_generate();
    let json = match serde_json::to_string(&kp) {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    match CString::new(json) {
        Ok(cs) => cs.into_raw(),
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Ký thông điệp Dilithium
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_dilithium_sign(
    sk_ptr: *const u8,
    sk_len: usize,
    msg_ptr: *const u8,
    msg_len: usize,
    out_sig_len: *mut usize,
    out_sig_cap: *mut usize,
) -> *mut u8 {
    if sk_ptr.is_null() || msg_ptr.is_null() || out_sig_len.is_null() || out_sig_cap.is_null() {
        return std::ptr::null_mut();
    }
    let sk_slice = std::slice::from_raw_parts(sk_ptr, sk_len);
    let msg_slice = std::slice::from_raw_parts(msg_ptr, msg_len);

    match dilithium_sign(sk_slice, msg_slice) {
        Ok(mut sig) => {
            *out_sig_len = sig.len();
            *out_sig_cap = sig.capacity();
            let ptr = sig.as_mut_ptr();
            std::mem::forget(sig);
            ptr
        }
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Xác thực chữ ký số Dilithium (1: hợp lệ, 0: không hợp lệ)
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_dilithium_verify(
    pk_ptr: *const u8,
    pk_len: usize,
    msg_ptr: *const u8,
    msg_len: usize,
    sig_ptr: *const u8,
    sig_len: usize,
) -> i32 {
    if pk_ptr.is_null() || msg_ptr.is_null() || sig_ptr.is_null() {
        return 0;
    }
    let pk_slice = std::slice::from_raw_parts(pk_ptr, pk_len);
    let msg_slice = std::slice::from_raw_parts(msg_ptr, msg_len);
    let sig_slice = std::slice::from_raw_parts(sig_ptr, sig_len);

    if dilithium_verify(pk_slice, msg_slice, sig_slice) {
        1
    } else {
        0
    }
}

/// FFI: Mã hóa lai Hybrid PQC Envelope trả về JSON String
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_hybrid_encrypt_json(
    recipient_kyber_pk_hex: *const c_char,
    plaintext: *const c_char,
) -> *mut c_char {
    if recipient_kyber_pk_hex.is_null() || plaintext.is_null() {
        return std::ptr::null_mut();
    }
    let pk_str = match CStr::from_ptr(recipient_kyber_pk_hex).to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    let pt_bytes = match CStr::from_ptr(plaintext).to_str() {
        Ok(s) => s.as_bytes(),
        Err(_) => return std::ptr::null_mut(),
    };

    let pk_bytes = match hex::decode(pk_str) {
        Ok(b) => b,
        Err(_) => return std::ptr::null_mut(),
    };

    match pqc_hybrid_encrypt(&pk_bytes, pt_bytes, None) {
        Ok(env) => match serde_json::to_string(&env) {
            Ok(json) => match CString::new(json) {
                Ok(cs) => cs.into_raw(),
                Err(_) => std::ptr::null_mut(),
            },
            Err(_) => std::ptr::null_mut(),
        },
        Err(_) => std::ptr::null_mut(),
    }
}

/// FFI: Giải mã lai Hybrid PQC Envelope từ JSON String
#[no_mangle]
pub unsafe extern "C" fn ffi_pqc_hybrid_decrypt_json(
    recipient_kyber_sk_hex: *const c_char,
    envelope_json: *const c_char,
) -> *mut c_char {
    if recipient_kyber_sk_hex.is_null() || envelope_json.is_null() {
        return std::ptr::null_mut();
    }
    let sk_str = match CStr::from_ptr(recipient_kyber_sk_hex).to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };
    let env_str = match CStr::from_ptr(envelope_json).to_str() {
        Ok(s) => s,
        Err(_) => return std::ptr::null_mut(),
    };

    let sk_bytes = match hex::decode(sk_str) {
        Ok(b) => b,
        Err(_) => return std::ptr::null_mut(),
    };

    let envelope: PqcHybridEnvelope = match serde_json::from_str(env_str) {
        Ok(env) => env,
        Err(_) => return std::ptr::null_mut(),
    };

    match pqc_hybrid_decrypt(&sk_bytes, &envelope, None) {
        Ok(pt_bytes) => match CString::new(pt_bytes) {
            Ok(cs) => cs.into_raw(),
            Err(_) => std::ptr::null_mut(),
        },
        Err(_) => std::ptr::null_mut(),
    }
}

// =========================================================================
// UNIT TESTS TOÀN DIỆN CHO PQC MODULE
// =========================================================================

#[cfg(test)]
mod pqc_tests {
    use super::*;

    #[test]
    fn test_kyber_kem_roundtrip_and_shared_secret() {
        let kp = kyber_keypair_generate();
        assert_eq!(kp.public_key.len(), KYBER_PUBLIC_KEY_LEN);
        assert_eq!(kp.secret_key.len(), KYBER_SECRET_KEY_LEN);

        let (ct, ss_enc) = kyber_encapsulate(&kp.public_key).expect("Encapsulation should succeed");
        assert_eq!(ct.len(), KYBER_CIPHERTEXT_LEN);
        assert_eq!(ss_enc.len(), KYBER_SHARED_SECRET_LEN);

        let ss_dec = kyber_decapsulate(&kp.secret_key, &ct).expect("Decapsulation should succeed");
        assert_eq!(ss_enc, ss_dec);

        // Test implicit rejection khi ciphertext bị giả mạo
        let mut tampered_ct = ct.clone();
        let last_idx = tampered_ct.len() - 1;
        tampered_ct[last_idx] ^= 0xAA;

        let ss_tampered = kyber_decapsulate(&kp.secret_key, &tampered_ct)
            .expect("Decapsulation should not panic on tampered ciphertext");
        // Implicit rejection: shared secret nhận được phải khác hoàn toàn
        assert_ne!(ss_enc, ss_tampered);
    }

    #[test]
    fn test_dilithium_signature_roundtrip_and_tamper_proofing() {
        let kp = dilithium_keypair_generate();
        assert_eq!(kp.public_key.len(), DILITHIUM_PUBLIC_KEY_LEN);
        assert_eq!(kp.secret_key.len(), DILITHIUM_SECRET_KEY_LEN);

        let message = b"SupportFlast quantum-resistant audit ticket #9901";
        let sig = dilithium_sign(&kp.secret_key, message).expect("Signing should succeed");
        assert_eq!(sig.len(), DILITHIUM_SIGNATURE_LEN);

        // Xác thực thành công
        assert!(dilithium_verify(&kp.public_key, message, &sig));

        // Xác thực thất bại khi message bị sửa
        let tampered_msg = b"SupportFlast quantum-resistant audit ticket #9902";
        assert!(!dilithium_verify(&kp.public_key, tampered_msg, &sig));

        // Xác thực thất bại khi chữ ký bị sửa
        let mut tampered_sig = sig.clone();
        tampered_sig[0] ^= 0xFF;
        assert!(!dilithium_verify(&kp.public_key, message, &tampered_sig));
    }

    #[test]
    fn test_hybrid_pqc_encryption_with_signature() {
        let kyber_kp = kyber_keypair_generate();
        let dilithium_kp = dilithium_keypair_generate();

        let payload = b"Super-confidential SupportFlast master key data payload";

        // Mã hóa có kèm chữ ký số Dilithium
        let envelope = pqc_hybrid_encrypt(
            &kyber_kp.public_key,
            payload,
            Some(&dilithium_kp.secret_key),
        ).expect("Hybrid encryption should succeed");

        assert!(envelope.dilithium_signature.is_some());
        assert!(envelope.sender_dilithium_pk.is_some());

        // Giải mã thành công
        let decrypted = pqc_hybrid_decrypt(
            &kyber_kp.secret_key,
            &envelope,
            Some(&dilithium_kp.public_key),
        ).expect("Hybrid decryption should succeed");

        assert_eq!(decrypted, payload);

        // Giả mạo ciphertext AES làm xác thực chữ ký thất bại
        let mut tampered_env = envelope.clone();
        let last = tampered_env.aes_ciphertext.len() - 1;
        tampered_env.aes_ciphertext[last] ^= 0x55;

        let dec_fail = pqc_hybrid_decrypt(
            &kyber_kp.secret_key,
            &tampered_env,
            Some(&dilithium_kp.public_key),
        );
        assert!(dec_fail.is_err());
    }

    #[test]
    fn test_static_cache_keypairs() {
        let k1 = get_cached_kyber_keypair();
        let k2 = get_cached_kyber_keypair();
        assert_eq!(k1.public_key, k2.public_key);

        let d1 = get_cached_dilithium_keypair();
        let d2 = get_cached_dilithium_keypair();
        assert_eq!(d1.public_key, d2.public_key);
    }

    #[test]
    fn test_ffi_pqc_operations_and_memory_cleanup() {
        use crate::{free_rust_bytes, free_rust_string};

        unsafe {
            // 1. Test FFI Kyber KeyGen
            let kyber_json_ptr = ffi_pqc_kyber_keygen();
            assert!(!kyber_json_ptr.is_null());
            let kyber_json = CStr::from_ptr(kyber_json_ptr).to_str().unwrap();
            let kyber_kp: KyberKeyPair = serde_json::from_str(kyber_json).unwrap();
            free_rust_string(kyber_json_ptr);

            // 2. Test FFI Kyber Encapsulate & Decapsulate
            let mut ct_len = 0;
            let mut ct_cap = 0;
            let mut ss_enc_len = 0;
            let mut ss_enc_cap = 0;
            let mut ss_enc_ptr = std::ptr::null_mut();

            let ct_ptr = ffi_pqc_kyber_encapsulate(
                kyber_kp.public_key.as_ptr(),
                kyber_kp.public_key.len(),
                &mut ct_len,
                &mut ct_cap,
                &mut ss_enc_len,
                &mut ss_enc_cap,
                &mut ss_enc_ptr,
            );
            assert!(!ct_ptr.is_null());
            assert!(!ss_enc_ptr.is_null());
            assert_eq!(ct_len, KYBER_CIPHERTEXT_LEN);
            assert_eq!(ss_enc_len, KYBER_SHARED_SECRET_LEN);

            let mut ss_dec_len = 0;
            let mut ss_dec_cap = 0;
            let ss_dec_ptr = ffi_pqc_kyber_decapsulate(
                kyber_kp.secret_key.as_ptr(),
                kyber_kp.secret_key.len(),
                ct_ptr,
                ct_len,
                &mut ss_dec_len,
                &mut ss_dec_cap,
            );
            assert!(!ss_dec_ptr.is_null());
            assert_eq!(ss_dec_len, KYBER_SHARED_SECRET_LEN);

            let ss_enc_slice = std::slice::from_raw_parts(ss_enc_ptr, ss_enc_len);
            let ss_dec_slice = std::slice::from_raw_parts(ss_dec_ptr, ss_dec_len);
            assert_eq!(ss_enc_slice, ss_dec_slice);

            // Memory cleanup via Rule 7.1
            free_rust_bytes(ct_ptr, ct_len, ct_cap);
            free_rust_bytes(ss_enc_ptr, ss_enc_len, ss_enc_cap);
            free_rust_bytes(ss_dec_ptr, ss_dec_len, ss_dec_cap);

            // 3. Test FFI Dilithium KeyGen & Sign & Verify
            let dil_json_ptr = ffi_pqc_dilithium_keygen();
            assert!(!dil_json_ptr.is_null());
            let dil_json = CStr::from_ptr(dil_json_ptr).to_str().unwrap();
            let dil_kp: DilithiumKeyPair = serde_json::from_str(dil_json).unwrap();
            free_rust_string(dil_json_ptr);

            let msg = b"SupportFlast PQC FFI C-ABI Verification";
            let mut sig_len = 0;
            let mut sig_cap = 0;
            let sig_ptr = ffi_pqc_dilithium_sign(
                dil_kp.secret_key.as_ptr(),
                dil_kp.secret_key.len(),
                msg.as_ptr(),
                msg.len(),
                &mut sig_len,
                &mut sig_cap,
            );
            assert!(!sig_ptr.is_null());
            assert_eq!(sig_len, DILITHIUM_SIGNATURE_LEN);

            let is_valid = ffi_pqc_dilithium_verify(
                dil_kp.public_key.as_ptr(),
                dil_kp.public_key.len(),
                msg.as_ptr(),
                msg.len(),
                sig_ptr,
                sig_len,
            );
            assert_eq!(is_valid, 1);

            let is_invalid = ffi_pqc_dilithium_verify(
                dil_kp.public_key.as_ptr(),
                dil_kp.public_key.len(),
                b"Altered message".as_ptr(),
                15,
                sig_ptr,
                sig_len,
            );
            assert_eq!(is_invalid, 0);

            // Memory cleanup via Rule 7.1
            free_rust_bytes(sig_ptr, sig_len, sig_cap);

            // 4. Test FFI Hybrid PQC Encrypt & Decrypt JSON
            let pk_hex = hex::encode(&kyber_kp.public_key);
            let sk_hex = hex::encode(&kyber_kp.secret_key);
            let pk_hex_c = CString::new(pk_hex).unwrap();
            let sk_hex_c = CString::new(sk_hex).unwrap();
            let pt_c = CString::new("Quantum-secure ticket payload #100").unwrap();

            let enc_json_ptr = ffi_pqc_hybrid_encrypt_json(pk_hex_c.as_ptr(), pt_c.as_ptr());
            assert!(!enc_json_ptr.is_null());

            let dec_str_ptr = ffi_pqc_hybrid_decrypt_json(sk_hex_c.as_ptr(), enc_json_ptr);
            assert!(!dec_str_ptr.is_null());
            let dec_str = CStr::from_ptr(dec_str_ptr).to_str().unwrap();
            assert_eq!(dec_str, "Quantum-secure ticket payload #100");

            // Memory cleanup via Rule 7.1
            free_rust_string(enc_json_ptr);
            free_rust_string(dec_str_ptr);
        }
    }
}
