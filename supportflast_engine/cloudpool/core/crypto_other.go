//go:build !windows

package core

func initRustDLL() {}

func tryRustDeriveKey(passphrase string, outKey *[32]byte) bool {
	return false
}

func tryRustEncrypt(key [32]byte, plaintext []byte) ([]byte, bool) {
	return nil, false
}

func tryRustDecrypt(key [32]byte, encryptedData []byte) ([]byte, bool) {
	return nil, false
}

func tryRustDecryptInPlace(key [32]byte, encryptedData []byte) ([]byte, bool) {
	return nil, false
}

func tryRustHashChunk(data []byte) (string, bool) {
	return "", false
}

func tryRustPqcKyberKeygen() ([]byte, []byte, bool) {
	return nil, nil, false
}

func tryRustPqcKyberEncapsulate(pk []byte) ([]byte, []byte, bool) {
	return nil, nil, false
}

func tryRustPqcKyberDecapsulate(sk []byte, ct []byte) ([]byte, bool) {
	return nil, false
}

func tryRustPqcDilithiumKeygen() ([]byte, []byte, bool) {
	return nil, nil, false
}

func tryRustPqcDilithiumSign(sk []byte, msg []byte) ([]byte, bool) {
	return nil, false
}

func tryRustPqcDilithiumVerify(pk []byte, msg []byte, sig []byte) (bool, bool) {
	return false, false
}

func tryRustPqcHybridEncrypt(recipientKyberPKHex string, plaintext string) (string, bool) {
	return "", false
}

func tryRustPqcHybridDecrypt(recipientKyberSKHex string, envelopeJSON string) (string, bool) {
	return "", false
}

func isDLLLoaded() bool {
	return false
}
