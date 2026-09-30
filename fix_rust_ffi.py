import io

path = r'f:\supportflast.dev\supportflast_core\src\lib.rs'
with io.open(path, 'r', encoding='utf-8') as f:
    content = f.read()

zero_copy_fn = '''
use aes_gcm::aead::AeadInPlace;

#[no_mangle]
pub unsafe extern "C" fn decrypt_chunk_in_place_ffi(
    key_ptr: *const u8,
    data_ptr: *mut u8,
    data_len: usize,
    out_plaintext_len: *mut usize,
) -> i32 {
    if key_ptr.is_null() || data_ptr.is_null() || out_plaintext_len.is_null() {
        return -1;
    }
    if data_len < NONCE_LEN + 16 {
        return -1;
    }

    let key_slice = std::slice::from_raw_parts(key_ptr, KEY_LEN);
    let cipher = match Aes256Gcm::new_from_slice(key_slice) {
        Ok(c) => c,
        Err(_) => return -2,
    };

    let nonce = Nonce::from_slice(std::slice::from_raw_parts(data_ptr, NONCE_LEN));
    let tag_pos = data_len - 16;
    let tag = aes_gcm::Tag::from_slice(std::slice::from_raw_parts(data_ptr.add(tag_pos), 16));
    let ct_len = tag_pos - NONCE_LEN;
    let ct_slice = std::slice::from_raw_parts_mut(data_ptr.add(NONCE_LEN), ct_len);

    match cipher.decrypt_in_place_detached(nonce, b"", ct_slice, tag) {
        Ok(()) => {
            *out_plaintext_len = ct_len;
            0
        }
        Err(_) => -3,
    }
}
'''

if 'decrypt_chunk_in_place_ffi' not in content:
    content += '\n' + zero_copy_fn

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(content)
