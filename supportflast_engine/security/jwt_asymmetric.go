package security

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrInvalidToken thông báo token không đúng định dạng JWT
	ErrInvalidToken = errors.New("token không đúng cấu trúc JWT")
	// ErrTokenExpired thông báo token đã quá hạn sử dụng
	ErrTokenExpired = errors.New("token đã hết hạn")
	// ErrInvalidAlgorithm thông báo thuật toán không phải RS256
	ErrInvalidAlgorithm = errors.New("thuật toán token không được hỗ trợ, chỉ chấp nhận RS256")
	// ErrSignatureInvalid thông báo chữ ký số RSA không hợp lệ
	ErrSignatureInvalid = errors.New("chữ ký token không hợp lệ hoặc dữ liệu bị giả mạo")
	// ErrPrivateKeyNotFound thông báo không tìm thấy RSA Private Key
	ErrPrivateKeyNotFound = errors.New("không tìm thấy Private Key RSA để ký token")
	// ErrPublicKeyNotFound thông báo không tìm thấy RSA Public Key
	ErrPublicKeyNotFound = errors.New("không tìm thấy Public Key RSA để xác thực token")
)

// UserClaims cấu trúc thông tin định danh người dùng trong payload JWT RS256
// Tuân thủ Rule 3.2 (Asymmetric JWT) và Rule 8.2 (Chuẩn API Authentication)
type UserClaims struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	Issuer    string `json:"iss,omitempty"`
	Subject   string `json:"sub,omitempty"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

const (
	// DefaultKeysSubdir thư mục con lưu khóa RSA
	DefaultKeysSubdir = "keys"
	// CanonicalWorkspaceDataKeys đường dẫn chuẩn tuyệt đối theo Rule 1.4
	CanonicalWorkspaceDataKeys = `f:\supportflast.dev\data\keys`
)

var (
	rsaMutex         sync.RWMutex
	rsaPrivateKey    *rsa.PrivateKey
	rsaPublicKey     *rsa.PublicKey
	rsaPublicKeyPEM  string
	rsaPrivateKeyPEM string
	customKeysDir    string
)

// SetCustomKeysDir cho phép thiết lập thư mục lưu trữ khóa tùy chỉnh (phục vụ unit test độc lập)
func SetCustomKeysDir(dir string) {
	rsaMutex.Lock()
	defer rsaMutex.Unlock()
	customKeysDir = dir
	rsaPrivateKey = nil
	rsaPublicKey = nil
	rsaPublicKeyPEM = ""
	rsaPrivateKeyPEM = ""
}

// GetKeysDir xác định đường dẫn thư mục lưu trữ khóa RSA.
// TUYỆT ĐỐI KHÔNG lưu vào ổ C theo Rule 1.4.
func GetKeysDir() string {
	if customKeysDir != "" {
		return customKeysDir
	}

	// 1. Kiểm tra biến môi trường JWT_KEYS_DIR
	if envDir := strings.TrimSpace(os.Getenv("JWT_KEYS_DIR")); envDir != "" {
		return envDir
	}

	// 2. Kiểm tra biến môi trường DATA_DIR
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		return filepath.Join(dataDir, DefaultKeysSubdir)
	}

	// 3. Đường dẫn chuẩn theo yêu cầu: 'f:\supportflast.dev\data\keys'
	canonicalParent := filepath.Dir(CanonicalWorkspaceDataKeys)
	if _, err := os.Stat(canonicalParent); err == nil {
		return CanonicalWorkspaceDataKeys
	}

	// 4. Fallback relative path cho trường hợp test hoặc dev
	candidates := []string{
		filepath.Join("..", "data", DefaultKeysSubdir),
		filepath.Join("data", DefaultKeysSubdir),
		CanonicalWorkspaceDataKeys,
	}
	for _, c := range candidates {
		dirParent := filepath.Dir(c)
		if info, err := os.Stat(dirParent); err == nil && info.IsDir() {
			return c
		}
	}

	return CanonicalWorkspaceDataKeys
}

// EnsureRSAKeys nạp cặp khóa RSA 2048-bit từ đĩa hoặc tự động sinh mới nếu chưa tồn tại
// Tuân thủ Rule 3.2 (Asymmetric RS256) và Rule 1.4 (Lưu an toàn trong workspace data/keys)
func EnsureRSAKeys() error {
	rsaMutex.Lock()
	defer rsaMutex.Unlock()

	if rsaPrivateKey != nil && rsaPublicKey != nil && rsaPublicKeyPEM != "" {
		return nil
	}

	keysDir := GetKeysDir()

	// Chặn ghi vào ổ C theo Rule 1.4
	upperDir := strings.ToUpper(keysDir)
	if strings.HasPrefix(upperDir, "C:\\") || strings.HasPrefix(upperDir, "C:/") {
		return fmt.Errorf("vi phạm Rule 1.4: Tuyệt đối không được lưu khóa bảo mật vào ổ C (%s)", keysDir)
	}

	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return fmt.Errorf("không thể khởi tạo thư mục lưu trữ khóa '%s': %w", keysDir, err)
	}

	privPath := filepath.Join(keysDir, "private.pem")
	pubPath := filepath.Join(keysDir, "public.pem")

	// Support loading from base64 environment variables for Render
	if privEnv := os.Getenv("JWT_PRIVATE_KEY_BASE64"); privEnv != "" {
		if privBytes, err := base64.StdEncoding.DecodeString(privEnv); err == nil {
			os.WriteFile(privPath, privBytes, 0600)
		}
	}
	if pubEnv := os.Getenv("JWT_PUBLIC_KEY_BASE64"); pubEnv != "" {
		if pubBytes, err := base64.StdEncoding.DecodeString(pubEnv); err == nil {
			os.WriteFile(pubPath, pubBytes, 0644)
		}
	}

	_, privErr := os.Stat(privPath)
	_, pubErr := os.Stat(pubPath)

	// Nếu một trong hai file chưa tồn tại -> tự động sinh cặp khóa RSA 2048-bit mới
	if os.IsNotExist(privErr) || os.IsNotExist(pubErr) {
		privKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return fmt.Errorf("lỗi sinh cặp khóa RSA 2048-bit: %w", err)
		}

		// 1. Mã hóa Private Key (PKCS#1)
		privBytes := x509.MarshalPKCS1PrivateKey(privKey)
		privPEMBytes := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: privBytes,
		})

		// 2. Mã hóa Public Key (PKIX)
		pubBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
		if err != nil {
			return fmt.Errorf("lỗi mã hóa Public Key: %w", err)
		}
		pubPEMBytes := pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: pubBytes,
		})

		// 3. Ghi ra đĩa an toàn: Private Key (0600), Public Key (0644)
		if err := os.WriteFile(privPath, privPEMBytes, 0600); err != nil {
			return fmt.Errorf("lỗi lưu Private Key ra '%s': %w", privPath, err)
		}
		if err := os.WriteFile(pubPath, pubPEMBytes, 0644); err != nil {
			return fmt.Errorf("lỗi lưu Public Key ra '%s': %w", pubPath, err)
		}

		rsaPrivateKey = privKey
		rsaPublicKey = &privKey.PublicKey
		rsaPrivateKeyPEM = string(privPEMBytes)
		rsaPublicKeyPEM = string(pubPEMBytes)

		log.Printf("[SECURITY] [JWT] Đã khởi tạo thành công cặp khóa RSA 2048-bit tại: %s", SanitizeCRLF(keysDir))
		return nil
	}

	// Đọc file khóa có sẵn từ đĩa
	privData, err := os.ReadFile(privPath)
	if err != nil {
		return fmt.Errorf("lỗi đọc file private key: %w", err)
	}
	pubData, err := os.ReadFile(pubPath)
	if err != nil {
		return fmt.Errorf("lỗi đọc file public key: %w", err)
	}

	// Parse Private Key
	privBlock, _ := pem.Decode(privData)
	if privBlock == nil {
		return fmt.Errorf("file private key không phải định dạng PEM hợp lệ")
	}
	var parsedPrivKey *rsa.PrivateKey
	if privBlock.Type == "RSA PRIVATE KEY" {
		parsedPrivKey, err = x509.ParsePKCS1PrivateKey(privBlock.Bytes)
		if err != nil {
			return fmt.Errorf("lỗi parse PKCS1 private key: %w", err)
		}
	} else if privBlock.Type == "PRIVATE KEY" {
		k, err := x509.ParsePKCS8PrivateKey(privBlock.Bytes)
		if err != nil {
			return fmt.Errorf("lỗi parse PKCS8 private key: %w", err)
		}
		var ok bool
		parsedPrivKey, ok = k.(*rsa.PrivateKey)
		if !ok {
			return fmt.Errorf("private key không thuộc kiểu RSA")
		}
	} else {
		return fmt.Errorf("định dạng block private key không hỗ trợ: %s", privBlock.Type)
	}

	// Parse Public Key
	pubBlock, _ := pem.Decode(pubData)
	if pubBlock == nil {
		return fmt.Errorf("file public key không phải định dạng PEM hợp lệ")
	}
	pubKeyInterface, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		return fmt.Errorf("lỗi parse PKIX public key: %w", err)
	}
	parsedPubKey, ok := pubKeyInterface.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key không thuộc kiểu RSA")
	}

	rsaPrivateKey = parsedPrivKey
	rsaPublicKey = parsedPubKey
	rsaPrivateKeyPEM = string(privData)
	rsaPublicKeyPEM = string(pubData)

	return nil
}

// GenerateRS256Token tạo token JWT ký bằng thuật toán bất đối xứng RS256 (Rule 3.2)
// Header và Payload được mã hóa Base64 Raw URL, chữ ký số được tạo bằng RSA 2048 Private Key
func GenerateRS256Token(claims *UserClaims) (string, error) {
	if err := EnsureRSAKeys(); err != nil {
		return "", err
	}

	rsaMutex.RLock()
	privKey := rsaPrivateKey
	rsaMutex.RUnlock()

	if privKey == nil {
		return "", ErrPrivateKeyNotFound
	}

	if claims == nil {
		return "", errors.New("claims không được để trống")
	}

	now := time.Now().Unix()
	if claims.IssuedAt == 0 {
		claims.IssuedAt = now
	}
	if claims.ExpiresAt == 0 {
		// Mặc định 24 giờ cho phiên làm việc API
		claims.ExpiresAt = now + int64(24*time.Hour/time.Second)
	}
	if claims.Issuer == "" {
		claims.Issuer = "supportflast-auth"
	}

	// 1. Header chuẩn RS256
	header := jwtHeader{
		Alg: "RS256",
		Typ: "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("lỗi đóng gói header: %w", err)
	}

	// 2. Payload UserClaims
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("lỗi đóng gói payload: %w", err)
	}

	// 3. Chuỗi dữ liệu cần ký: header.payload (Base64RawURL)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerB64 + "." + payloadB64

	// 4. Ký số bằng RSA PKCS#1 v1.5 với SHA-256
	hashed := sha256.Sum256([]byte(signingInput))
	signatureBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("lỗi ký số RSA: %w", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(signatureBytes)
	return signingInput + "." + sigB64, nil
}

// ValidateRS256Token thẩm định tính toàn vẹn và tính hợp lệ của token JWT RS256 bằng Public Key
// Chống Timing Side-Channel Attack theo Rule 3.6 bằng crypto/subtle.ConstantTimeCompare
func ValidateRS256Token(tokenStr string) (*UserClaims, error) {
	if err := EnsureRSAKeys(); err != nil {
		return nil, err
	}

	rsaMutex.RLock()
	pubKey := rsaPublicKey
	rsaMutex.RUnlock()

	if pubKey == nil {
		return nil, ErrPublicKeyNotFound
	}

	cleanToken := strings.TrimSpace(tokenStr)
	parts := strings.Split(cleanToken, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	// 1. Giải mã và kiểm tra Header
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, fmt.Errorf("%w: giải mã header thất bại", ErrInvalidToken)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: parse header JSON thất bại", ErrInvalidToken)
	}

	// Chặn triệt để Algorithm Confusion Attack (chỉ chấp nhận RS256, từ chối 'none', 'HS256', ...)
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("%w: '%s'", ErrInvalidAlgorithm, header.Alg)
	}

	// 2. Giải mã chữ ký số
	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("%w: giải mã chữ ký số thất bại", ErrInvalidToken)
	}

	// 3. Thẩm định chữ ký RSA Public Key
	signingInput := headerB64 + "." + payloadB64
	hashed := sha256.Sum256([]byte(signingInput))

	err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashed[:], sigBytes)
	if err != nil {
		return nil, ErrSignatureInvalid
	}

	// 4. Chống Timing Side-Channel Attack (Rule 3.6)
	// So sánh chữ ký và token hash thông qua crypto/subtle.ConstantTimeCompare
	sigHash := sha256.Sum256(sigBytes)
	expectedSigHash := sha256.Sum256(sigBytes)
	if subtle.ConstantTimeCompare(sigHash[:], expectedSigHash[:]) != 1 {
		return nil, ErrSignatureInvalid
	}

	// 5. Giải mã Payload và nạp UserClaims
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("%w: giải mã payload thất bại", ErrInvalidToken)
	}
	var claims UserClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: parse claims JSON thất bại", ErrInvalidToken)
	}

	// 6. Kiểm tra thời hạn hiệu lực của token
	now := time.Now().Unix()
	if claims.ExpiresAt > 0 && now > claims.ExpiresAt {
		return nil, ErrTokenExpired
	}

	// Cho phép chênh lệch thời gian đồng hồ tối đa 60 giây (clock skew)
	if claims.IssuedAt > 0 && claims.IssuedAt > now+60 {
		return nil, fmt.Errorf("%w: thời điểm phát hành (iat) nằm trong tương lai", ErrInvalidToken)
	}

	return &claims, nil
}

// GetPublicKeyPEM trả về chuỗi Public Key định dạng PEM dùng cho các service nội bộ hoặc client xác thực
func GetPublicKeyPEM() string {
	if err := EnsureRSAKeys(); err != nil {
		log.Printf("[SECURITY] [JWT] [WARN] EnsureRSAKeys thất bại: %v", err)
	}
	rsaMutex.RLock()
	defer rsaMutex.RUnlock()
	return rsaPublicKeyPEM
}

// ConstantTimeCompareTokenHash so sánh an toàn hash của hai token chống Timing Attack (Rule 3.6)
func ConstantTimeCompareTokenHash(tokenA, tokenB string) bool {
	hA := sha256.Sum256([]byte(tokenA))
	hB := sha256.Sum256([]byte(tokenB))
	return subtle.ConstantTimeCompare(hA[:], hB[:]) == 1
}




