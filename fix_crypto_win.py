import io

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\core\crypto_windows.go'
with io.open(path, 'r', encoding='utf-8') as f:
    content = f.read()

if "procDecryptInPlace" not in content:
    # Add procDecryptInPlace declaration
    content = content.replace("procFreeBuffer *syscall.LazyProc", "procFreeBuffer *syscall.LazyProc\n\tprocDecryptInPlace *syscall.LazyProc")
    
    # Add init binding
    content = content.replace('procFreeBuffer = rustDLL.NewProc("free_rust_buffer")', 'procFreeBuffer = rustDLL.NewProc("free_rust_buffer")\n\t\tprocDecryptInPlace = rustDLL.NewProc("decrypt_chunk_in_place_ffi")')

    # Add tryRustDecryptInPlace function
    in_place_fn = """
// tryRustDecryptInPlace thuc hien giai ma in-place Zero-Copy
func tryRustDecryptInPlace(key [32]byte, encryptedData []byte) ([]byte, bool) {
	initRustDLL()
	if !dllLoaded || procDecryptInPlace == nil || procDecryptInPlace.Find() != nil || len(encryptedData) < 28 {
		return nil, false
	}
	var ptLen uintptr
	r1, _, _ := procDecryptInPlace.Call(
		uintptr(unsafe.Pointer(&key[0])),
		uintptr(unsafe.Pointer(&encryptedData[0])),
		uintptr(len(encryptedData)),
		uintptr(unsafe.Pointer(&ptLen)),
	)
	if r1 == 0 && ptLen > 0 {
		return encryptedData[12 : 12+ptLen], true
	}
	return nil, false
}
"""
    content = content + in_place_fn
    
    # Replace goBytesAndFree logic with intrinsic copy
    old_goBytes = """	bytes := make([]byte, int(len))
	procMoveMemory.Call(
		uintptr(unsafe.Pointer(&bytes[0])),
		ptr,
		len,
	)
	procFreeBuffer.Call(ptr, len, cap)
	return bytes"""
    new_goBytes = """	bytes := make([]byte, int(len))
	src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(len))
	copy(bytes, src)
	procFreeBuffer.Call(ptr, len, cap)
	return bytes"""
    content = content.replace(old_goBytes, new_goBytes)

    with io.open(path, 'w', encoding='utf-8') as f:
        f.write(content)

