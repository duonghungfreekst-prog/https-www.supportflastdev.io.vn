import io

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\core\crypto.go'
with io.open(path, 'r', encoding='utf-8') as f:
    content = f.read()

# Replace DecryptChunk logic
if "tryRustDecryptInPlace" not in content:
    old_decrypt = """	if decSlice, ok := tryRustDecrypt(key, encryptedData); ok {
		return decSlice, nil
	}"""
    
    new_decrypt = """	// 1. Uu tien Zero-Copy In-Place (0 cap phat, 0 memcpy, cuc nhanh)
	if decSlice, ok := tryRustDecryptInPlace(key, encryptedData); ok {
		return decSlice, nil
	}

	// 2. Fallback sang FFI cu (neu DLL chua co ham in-place hoac OS khac)
	if decSlice, ok := tryRustDecrypt(key, encryptedData); ok {
		return decSlice, nil
	}"""
    content = content.replace(old_decrypt, new_decrypt)
    with io.open(path, 'w', encoding='utf-8') as f:
        f.write(content)
