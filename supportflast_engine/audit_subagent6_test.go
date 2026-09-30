package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/config"
	"supportflast_engine/database"

	_ "github.com/go-sql-driver/mysql"
)

func maskStr(s string) string {
	if s == "" {
		return "<rỗng>"
	}
	if len(s) <= 6 {
		return s[:1] + "****" + s[len(s)-1:]
	}
	return fmt.Sprintf("%s****%s (độ dài %d)", s[:3], s[len(s)-3:], len(s))
}

func TestSubagent6Audit(t *testing.T) {
	// Khởi tạo biến môi trường từ .env
	if err := config.InitEnv(); err != nil {
		t.Logf("[WARN] config.InitEnv error: %v", err)
	}

	envMasterKey := config.Get("CLOUDPOOL_MASTER_KEY", "")
	t.Logf("=== 1. KIỂM TRA .ENV & KEYS.JSON ===")
	t.Logf("  .env CLOUDPOOL_MASTER_KEY: %s", maskStr(envMasterKey))

	// Kiểm tra data/keys.json
	keysJsonPath := filepath.Join("..", "data", "keys.json")
	if _, err := os.Stat(keysJsonPath); err == nil {
		content, err := os.ReadFile(keysJsonPath)
		if err == nil {
			var keysList []map[string]interface{}
			if err := json.Unmarshal(content, &keysList); err == nil {
				t.Logf("  data/keys.json tồn tại, số lượng key: %d", len(keysList))
				for _, k := range keysList {
					name, _ := k["name"].(string)
					prefix, _ := k["prefix"].(string)
					status, _ := k["status"].(string)
					t.Logf("    Key: name='%s', prefix='%s', status='%s'", name, prefix, status)
				}
			}
		}
	} else {
		t.Logf("  data/keys.json không tồn tại: %v", err)
	}

	t.Logf("\n=== 2. KIỂM TRA SQLITE (data/cloudpool_metadata.db) ===")
	sqlitePath := filepath.Join("..", "data", "cloudpool_metadata.db")
	if _, err := os.Stat(sqlitePath); os.IsNotExist(err) {
		t.Skip("Bỏ qua: không tìm thấy SQLite metadata")
	}
	sqlDB, err := sql.Open("sqlite", sqlitePath+"?mode=ro")
	var sqliteMasterPass string
	if err != nil {
		t.Fatalf("Không thể mở SQLite: %v", err)
	}
	defer sqlDB.Close()

	_ = sqlDB.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&sqliteMasterPass)
	t.Logf("  SQLite settings.master_passphrase: %s", maskStr(sqliteMasterPass))

	sqliteKey := core.DeriveKey(sqliteMasterPass, nil)
	envKey := core.DeriveKey(envMasterKey, nil)
	defaultKey := core.DeriveKey("cloudpool_secure_master_key_2026", nil)

	t.Logf("  Khóa AES từ SQLite master_passphrase: deriveKey OK")
	t.Logf("  Khóa AES từ .env CLOUDPOOL_MASTER_KEY: deriveKey OK")

	// Kiểm tra Settings khác trong SQLite
	var gClientId, gClientSecret string
	_ = sqlDB.QueryRow("SELECT `value` FROM settings WHERE `key` = 'google_client_id'").Scan(&gClientId)
	_ = sqlDB.QueryRow("SELECT `value` FROM settings WHERE `key` = 'google_client_secret'").Scan(&gClientSecret)

	decGClientId := core.DecryptSecret(sqliteKey, gClientId)
	decGClientSecret := core.DecryptSecret(sqliteKey, gClientSecret)
	gClientDecOk := !strings.HasPrefix(decGClientId, "ENC:") && !strings.HasPrefix(decGClientSecret, "ENC:")
	t.Logf("  SQLite google_client_id/secret: Giải mã thành công: %v (Client ID len: %d)", gClientDecOk, len(decGClientId))

	// Kiểm tra Accounts trong SQLite
	rows, err := sqlDB.Query("SELECT id, email, email_hash, name, name_hash, token_json, credentials_json FROM accounts")
	if err != nil {
		t.Logf("  Lỗi query accounts SQLite: %v", err)
	} else {
		defer rows.Close()
		accCount := 0
		decSuccessCount := 0
		blindIndexMatchCount := 0
		tokenJsonValidCount := 0

		for rows.Next() {
			accCount++
			var id, encEmail, emailHash, encName, nameHash, encToken, encCreds string
			if err := rows.Scan(&id, &encEmail, &emailHash, &encName, &nameHash, &encToken, &encCreds); err != nil {
				continue
			}

			// Thử giải mã bằng SQLite master key
			decEmail := core.DecryptSecret(sqliteKey, encEmail)
			decName := core.DecryptSecret(sqliteKey, encName)
			decToken := core.DecryptSecret(sqliteKey, encToken)

			isDecrypted := !strings.HasPrefix(decEmail, "ENC:") && strings.Contains(decEmail, "@")
			if isDecrypted {
				decSuccessCount++
			}

			// Kiểm tra Blind Index
			computedEmailHash := core.BlindIndexHash(sqliteKey, decEmail)
			computedNameHash := core.BlindIndexHash(sqliteKey, decName)
			if computedEmailHash == emailHash && computedNameHash == nameHash {
				blindIndexMatchCount++
			}

			// Kiểm tra Token JSON
			var tokenObj map[string]interface{}
			if json.Unmarshal([]byte(decToken), &tokenObj) == nil {
				tokenJsonValidCount++
			}

			// Log mẫu 2 account đầu tiên
			if accCount <= 2 {
				t.Logf("    [Account %d] ID: %s | Decrypt: %v | BlindIndex: %v | TokenJSON: %v",
					accCount, id, isDecrypted, computedEmailHash == emailHash, tokenObj != nil)
			}
		}
		t.Logf("  Tổng accounts SQLite: %d | Giải mã thành công: %d | BlindIndex khớp: %d | TokenJSON hợp lệ: %d",
			accCount, decSuccessCount, blindIndexMatchCount, tokenJsonValidCount)
	}

	// Kiểm tra Users trong SQLite
	uRows, err := sqlDB.Query("SELECT id, username, email, display_name, username_hash, email_hash FROM users")
	if err != nil {
		t.Logf("  Lỗi query users SQLite: %v", err)
	} else {
		defer uRows.Close()
		uCount := 0
		uDecOk := 0
		for uRows.Next() {
			uCount++
			var uid, encU, encE, encD, uHash, eHash string
			if err := uRows.Scan(&uid, &encU, &encE, &encD, &uHash, &eHash); err == nil {
				decU := core.DecryptSecret(sqliteKey, encU)
				decE := core.DecryptSecret(sqliteKey, encE)
				if !strings.HasPrefix(decU, "ENC:") && !strings.HasPrefix(decE, "ENC:") {
					uDecOk++
				}
			}
		}
		t.Logf("  Tổng users SQLite: %d | Giải mã user thành công: %d", uCount, uDecOk)
	}

	// Kiểm tra VirtualFiles và FileChunks trong SQLite
	var vfCount, fcCount int
	_ = sqlDB.QueryRow("SELECT count(*) FROM virtual_files WHERE id != 'root'").Scan(&vfCount)
	_ = sqlDB.QueryRow("SELECT count(*) FROM file_chunks").Scan(&fcCount)
	t.Logf("  SQLite virtual_files: %d tệp | file_chunks: %d khối", vfCount, fcCount)

	t.Logf("\n=== 3. KIỂM TRA TIDB CLOUD ===")
	tidbCfg := database.DefaultTiDBConfig()
	t.Logf("  TiDB Host: %s | Port: %d | User: %s | DB: %s | TLS: %s",
		tidbCfg.Host, tidbCfg.Port, maskStr(tidbCfg.User), tidbCfg.Database, tidbCfg.TLS)

	var tidbMasterPass string
	tidbStore, err := storage.NewTiDB(tidbCfg)
	if err != nil {
		t.Logf("  [WARN] Không thể kết nối tới TiDB Cloud: %v", err)
	} else {
		defer tidbStore.Close()
		_ = tidbStore.SQLDB().QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&tidbMasterPass)
		t.Logf("  TiDB settings.master_passphrase: %s", maskStr(tidbMasterPass))

		tidbKey := core.DeriveKey(tidbMasterPass, nil)

		// Kiểm tra accounts trên TiDB
		tRows, err := tidbStore.SQLDB().Query("SELECT id, email, email_hash, name, name_hash, token_json, credentials_json FROM accounts")
		if err != nil {
			t.Logf("  Lỗi query accounts TiDB: %v", err)
		} else {
			defer tRows.Close()
			tAccCount := 0
			tDecSuccess := 0
			tBlindMatch := 0
			tTokenValid := 0

			for tRows.Next() {
				tAccCount++
				var id, encEmail, emailHash, encName, nameHash, encToken, encCreds string
				if err := tRows.Scan(&id, &encEmail, &emailHash, &encName, &nameHash, &encToken, &encCreds); err != nil {
					continue
				}

				decEmail := core.DecryptSecret(tidbKey, encEmail)
				decName := core.DecryptSecret(tidbKey, encName)
				decToken := core.DecryptSecret(tidbKey, encToken)

				isDec := !strings.HasPrefix(decEmail, "ENC:") && strings.Contains(decEmail, "@")
				if isDec {
					tDecSuccess++
				}

				cEmailHash := core.BlindIndexHash(tidbKey, decEmail)
				cNameHash := core.BlindIndexHash(tidbKey, decName)
				if cEmailHash == emailHash && cNameHash == nameHash {
					tBlindMatch++
				}

				var tokObj map[string]interface{}
				if json.Unmarshal([]byte(decToken), &tokObj) == nil {
					tTokenValid++
				}

				if tAccCount <= 2 {
					t.Logf("    [TiDB Account %d] ID: %s | Decrypt: %v | BlindIndex: %v | TokenJSON: %v",
						tAccCount, id, isDec, cEmailHash == emailHash, tokObj != nil)
				}
			}
			t.Logf("  Tổng accounts TiDB: %d | Giải mã thành công: %d | BlindIndex khớp: %d | TokenJSON hợp lệ: %d",
				tAccCount, tDecSuccess, tBlindMatch, tTokenValid)
		}

		var tVfCount, tFcCount int
		_ = tidbStore.SQLDB().QueryRow("SELECT count(*) FROM virtual_files WHERE id != 'root'").Scan(&tVfCount)
		_ = tidbStore.SQLDB().QueryRow("SELECT count(*) FROM file_chunks").Scan(&tFcCount)
		t.Logf("  TiDB virtual_files: %d tệp | file_chunks: %d khối", tVfCount, tFcCount)
	}

	t.Logf("\n=== 4. SO SÁNH KHÓA & TÍNH ĐỒNG BỘ ===")
	t.Logf("  SQLite Passphrase: %s", maskStr(sqliteMasterPass))
	t.Logf("  TiDB Passphrase:   %s", maskStr(tidbMasterPass))
	t.Logf("  .env Passphrase:   %s", maskStr(envMasterKey))

	if sqliteMasterPass == tidbMasterPass {
		t.Logf("  [ĐỒNG BỘ] SQLite và TiDB có cùng master_passphrase!")
	} else {
		t.Logf("  [LƯU Ý / KHÁC BIỆT] SQLite master_passphrase != TiDB master_passphrase!")
	}

	if envMasterKey == sqliteMasterPass {
		t.Logf("  [ĐỒNG BỘ] .env CLOUDPOOL_MASTER_KEY khớp với SQLite master_passphrase")
	} else {
		t.Logf("  [LƯU Ý / KHÁC BIỆT] .env CLOUDPOOL_MASTER_KEY != SQLite master_passphrase")
	}

	// 5. Thử mã hóa & giải mã chu kỳ AES-256-GCM và Blind Index với các khóa
	t.Logf("\n=== 5. KIỂM TRA CHU KỲ MÃ HÓA / GIẢI MÃ VỚI CORE CRYPTO ===")
	sampleSecret := "SuperSecretOAuthConfigPayload2026!@#"
	for keyName, k := range map[string][32]byte{
		"SQLiteKey":  sqliteKey,
		"EnvKey":     envKey,
		"DefaultKey": defaultKey,
	} {
		enc := core.EncryptSecret(k, sampleSecret)
		dec := core.DecryptSecret(k, enc)
		if dec != sampleSecret {
			t.Errorf("  Key %s: Lỗi giải mã chu kỳ! dec='%s'", keyName, dec)
		} else {
			t.Logf("  Key %s: Chu kỳ EncryptSecret -> DecryptSecret thành công 100%%", keyName)
		}

		bHash1 := core.BlindIndexHash(k, "test@supportflastdev.io.vn")
		bHash2 := core.BlindIndexHash(k, "TEST@supportflastdev.io.vn")
		if bHash1 != bHash2 {
			t.Errorf("  Key %s: BlindIndexHash không phân biệt hoa thường đúng!", keyName)
		} else {
			t.Logf("  Key %s: BlindIndexHash chuẩn xác (chống phân biệt hoa thường)", keyName)
		}
	}
}

// TestChunkCryptoIntegrity kiểm tra tính toàn vẹn của thuật toán mã hóa/giải mã AES-256-GCM trên file_chunks và virtual_files
func TestChunkCryptoIntegrity(t *testing.T) {
	sqlitePath := filepath.Join("..", "data", "cloudpool_metadata.db")
	if _, err := os.Stat(sqlitePath); os.IsNotExist(err) {
		t.Skip("Bỏ qua: không tìm thấy SQLite metadata")
	}
	sqlDB, err := sql.Open("sqlite", sqlitePath+"?mode=ro")
	if err != nil {
		t.Fatalf("Không thể mở SQLite: %v", err)
	}
	defer sqlDB.Close()

	var masterPass string
	_ = sqlDB.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&masterPass)
	if masterPass == "" {
		masterPass = "Hung1999@"
	}
	masterKey := core.DeriveKey(masterPass, nil)

	t.Logf("\n=== KIỂM TRA TÍNH TOÀN VẸN CẤU TRÚC FILE_CHUNKS ===")
	rows, err := sqlDB.Query("SELECT chunk_id, chunk_size_bytes, encrypted_size_bytes, sha256 FROM file_chunks LIMIT 20")
	if err != nil {
		t.Fatalf("Query file_chunks failed: %v", err)
	}
	defer rows.Close()

	checkedCount := 0
	sizeMatchCount := 0
	for rows.Next() {
		checkedCount++
		var cId, sha string
		var rawSz, encSz int64
		if err := rows.Scan(&cId, &rawSz, &encSz, &sha); err != nil {
			continue
		}
		// Trong AES-256-GCM: overhead là 28 bytes (12 bytes Nonce + 16 bytes Tag)
		expectedEncSz := rawSz + 28
		if encSz == expectedEncSz || (rawSz == 0 && encSz == 0) {
			sizeMatchCount++
		} else {
			t.Logf("  [WARN] Chunk %s: rawSz=%d, encSz=%d (overhead: %d != 28)", cId, rawSz, encSz, encSz-rawSz)
		}
	}
	t.Logf("  Kiểm tra %d chunks mẫu: %d chunks có kích thước mã hóa chuẩn AES-GCM (+28 bytes nonce/tag)", checkedCount, sizeMatchCount)

	// Kiểm tra chu trình mã hóa và giải mã thực tế với các kích thước chunk khác nhau
	t.Logf("\n=== KIỂM TRA CHU KỲ MÃ HÓA/GIẢI MÃ CHUNKS THỰC TẾ ===")
	testSizes := []int{64, 1024, 65536, 1048576, 5242880} // 64B, 1KB, 64KB, 1MB, 5MB
	for _, sz := range testSizes {
		rawChunk := make([]byte, sz)
		for i := 0; i < sz; i++ {
			rawChunk[i] = byte((i * 31) % 256)
		}

		encChunk, err := core.EncryptChunk(masterKey, rawChunk)
		if err != nil {
			t.Fatalf("Lỗi mã hóa chunk kích thước %d: %v", sz, err)
		}
		if len(encChunk) != sz+28 {
			t.Errorf("Kích thước chunk mã hóa không khớp! Mong đợi %d, nhận %d", sz+28, len(encChunk))
		}

		decChunk, err := core.DecryptChunk(masterKey, encChunk)
		if err != nil {
			t.Fatalf("Lỗi giải mã chunk kích thước %d: %v", sz, err)
		}
		if len(decChunk) != sz {
			t.Errorf("Kích thước chunk sau giải mã không khớp! Mong đợi %d, nhận %d", sz, len(decChunk))
		}

		// Kiểm tra tính toàn vẹn từng byte
		corrupt := false
		for i := 0; i < sz; i++ {
			if decChunk[i] != rawChunk[i] {
				corrupt = true
				break
			}
		}
		if corrupt {
			t.Errorf("Dữ liệu chunk kích thước %d bị sai lệch nội dung sau giải mã!", sz)
		} else {
			t.Logf("  Chunk %d bytes: Encrypt -> Decrypt toàn vẹn 100%% (overhead đúng 28 bytes)", sz)
		}

		// Kiểm tra an toàn: Giải mã bằng sai key phải bị từ chối, KHÔNG được ra chuỗi rác
		wrongKey := core.DeriveKey("wrong_passphrase_test_123", nil)
		_, errWrong := core.DecryptChunk(wrongKey, encChunk)
		if errWrong == nil {
			t.Errorf("NGUY HIỂM: Giải mã bằng wrongKey không báo lỗi!")
		}

		// Kiểm tra an toàn: Dữ liệu bị giả mạo / thay đổi 1 byte
		corruptedEnc := make([]byte, len(encChunk))
		copy(corruptedEnc, encChunk)
		corruptedEnc[len(corruptedEnc)-1] ^= 0xFF // Đảo bit tag
		_, errTamper := core.DecryptChunk(masterKey, corruptedEnc)
		if errTamper == nil {
			t.Errorf("NGUY HIỂM: Dữ liệu bị sửa đổi (tampered) nhưng giải mã không bắt lỗi!")
		}
	}
	t.Logf("  Đảm bảo an toàn: Thử giải mã sai khóa hoặc sửa đổi byte đều bị TỪ CHỐI (không sinh chuỗi rác).")
}

// TestCrossKeyDecryptionImpact kiểm tra ảnh hưởng khi master_passphrase bị sai lệch giữa .env, DB và Default
func TestCrossKeyDecryptionImpact(t *testing.T) {
	sqlitePath := filepath.Join("..", "data", "cloudpool_metadata.db")
	if _, err := os.Stat(sqlitePath); os.IsNotExist(err) {
		t.Skip("Bỏ qua: không tìm thấy SQLite metadata")
	}
	sqlDB, err := sql.Open("sqlite", sqlitePath+"?mode=ro")
	if err != nil {
		t.Fatalf("Không thể mở SQLite: %v", err)
	}
	defer sqlDB.Close()

	var dbPass string
	_ = sqlDB.QueryRow("SELECT `value` FROM settings WHERE `key` = 'master_passphrase'").Scan(&dbPass)

	envPass := config.Get("CLOUDPOOL_MASTER_KEY", "")
	defaultPass := "cloudpool_secure_master_key_2026"

	keyDB := core.DeriveKey(dbPass, nil)
	keyEnv := core.DeriveKey(envPass, nil)
	keyDefault := core.DeriveKey(defaultPass, nil)

	t.Logf("\n=== ĐÁNH GIÁ ẢNH HƯỞNG GIẢI MÃ KHI DÙNG CÁC KHÓA KHÁC NHAU ===")
	t.Logf("  Key A [DB settings]:      %s", maskStr(dbPass))
	t.Logf("  Key B [.env MASTER_KEY]:   %s", maskStr(envPass))
	t.Logf("  Key C [Code Default Key]:  %s", maskStr(defaultPass))

	// Lấy 1 tài khoản làm mẫu
	var encEmail, encName, encToken string
	err = sqlDB.QueryRow("SELECT email, name, token_json FROM accounts LIMIT 1").Scan(&encEmail, &encName, &encToken)
	if err != nil {
		t.Fatalf("Query account mẫu thất bại: %v", err)
	}

	keysMap := map[string][32]byte{
		"Key A (DB Settings)":     keyDB,
		"Key B (.env MASTER_KEY)": keyEnv,
		"Key C (Default Key)":     keyDefault,
	}

	for name, k := range keysMap {
		decEmail := core.DecryptSecret(k, encEmail)
		decName := core.DecryptSecret(k, encName)
		decToken := core.DecryptSecret(k, encToken)

		isEmailPlain := !strings.HasPrefix(decEmail, "ENC:") && strings.Contains(decEmail, "@")
		isNamePlain := !strings.HasPrefix(decName, "ENC:")
		var tokObj map[string]interface{}
		isTokenJson := json.Unmarshal([]byte(decToken), &tokObj) == nil

		t.Logf("  [%s]:", name)
		t.Logf("    - Email giải mã: thành công=%v (kết quả prefix: %s)", isEmailPlain, decEmail[:min(10, len(decEmail))])
		t.Logf("    - Name giải mã:  thành công=%v (kết quả prefix: %s)", isNamePlain, decName[:min(10, len(decName))])
		t.Logf("    - TokenJSON:     hợp lệ=%v", isTokenJson)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

