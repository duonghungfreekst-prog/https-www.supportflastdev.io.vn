package storage

import (
	"fmt"
	"log"
	"strings"
	"supportflast_engine/cloudpool/core"
)

// RekeyDatabase giáº£i mÃ£ toÃ n bá»™ dá»¯ liá»‡u mÃ£ hÃ³a báº±ng khÃ³a cÅ© vÃ  mÃ£ hÃ³a láº¡i báº±ng khÃ³a má»›i.
// ToÃ n bá»™ quÃ¡ trÃ¬nh Ä‘Æ°á»£c bá»c trong Transaction Ä‘á»ƒ Ä‘áº£m báº£o tÃ­nh nguyÃªn tá»­ (Atomicity).
func (s *DB) RekeyDatabase(oldPassphrase, newPassphrase string) error {
	if oldPassphrase == newPassphrase {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	oldKey := core.DeriveKey(oldPassphrase, nil)
	newKey := core.DeriveKey(newPassphrase, nil)

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("[ENGINE] [ERROR] RekeyDatabase: khÃ´ng thá»ƒ báº¯t Ä‘áº§u transaction: %v", err)
		return err
	}
	defer tx.Rollback()

	// HÃ m trá»£ giÃºp giáº£i mÃ£ báº±ng khÃ³a cÅ© vÃ  mÃ£ hÃ³a láº¡i báº±ng khÃ³a má»›i
	// Tuyá»‡t Ä‘á»‘i khÃ´ng nuá»‘t lá»—i Ã¢m tháº§m náº¿u dá»¯ liá»‡u báº¯t Ä‘áº§u báº±ng ENC: nhÆ°ng khÃ´ng giáº£i mÃ£ Ä‘Æ°á»£c
	processField := func(fieldName, recID, val string) (plain string, encrypted string, err error) {
		if !strings.HasPrefix(val, "ENC:") {
			return val, val, nil
		}
		pt := core.DecryptSecret(oldKey, val)
		if !strings.HasPrefix(pt, "ENC:") {
			return pt, core.EncryptSecret(newKey, pt), nil
		}

		// Fallback kiá»ƒm tra xem trÆ°á»ng nÃ y Ä‘Ã£ Ä‘Æ°á»£c mÃ£ hÃ³a báº±ng newKey tá»« trÆ°á»›c hay chÆ°a (trÃ¡nh lá»—i náº¿u rekey dá»Ÿ dang trÆ°á»›c Ä‘Ã³)
		ptNew := core.DecryptSecret(newKey, val)
		if !strings.HasPrefix(ptNew, "ENC:") {
			return ptNew, val, nil
		}

		// Cáº£ hai khÃ³a Ä‘á»u khÃ´ng giáº£i mÃ£ Ä‘Æ°á»£c -> Dá»¯ liá»‡u bá»‹ há»ng hoáº·c sai máº­t kháº©u
		log.Printf("[ENGINE] [WARN] RekeyDatabase: khÃ´ng thá»ƒ giáº£i mÃ£ trÆ°á»ng '%s' cho báº£n ghi ID '%s' (dá»¯ liá»‡u há»ng hoáº·c sai khÃ³a cÅ©)", fieldName, recID)
		return "", "", fmt.Errorf("rekey tháº¥t báº¡i táº¡i trÆ°á»ng '%s' (ID: %s): khÃ´ng thá»ƒ giáº£i mÃ£ dá»¯ liá»‡u ENC: báº±ng khÃ³a cÅ©", fieldName, recID)
	}

	// 1. Settings (google_client_id, google_client_secret, master_passphrase)
	setRows, err := tx.Query("SELECT `key`, `value` FROM settings WHERE `key` IN ('google_client_id', 'google_client_secret')")
	if err == nil {
		type setRec struct{ key, val string }
		var sRecs []setRec
		for setRows.Next() {
			var k, v string
			if err := setRows.Scan(&k, &v); err == nil {
				sRecs = append(sRecs, setRec{key: k, val: v})
			}
		}
		setRows.Close()

		for _, sr := range sRecs {
			if strings.HasPrefix(sr.val, "ENC:") {
				_, newEnc, err := processField(sr.key, "settings", sr.val)
				if err != nil {
					return err
				}
				if _, err := tx.Exec("UPDATE settings SET `value` = ? WHERE `key` = ?", newEnc, sr.key); err != nil {
					log.Printf("[ENGINE] [ERROR] RekeyDatabase: lỗi cập nhật setting '%s': %v", sr.key, err)
					return err
				}
			}
		}
	}
	var insSettingQuery string
	if s.IsMySQLOrTiDB() {
		insSettingQuery = "INSERT INTO settings (`key`, `value`) VALUES ('master_passphrase', ?) ON DUPLICATE KEY UPDATE `value`=VALUES(`value`)"
	} else {
		insSettingQuery = "INSERT INTO settings (key, value) VALUES ('master_passphrase', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value"
	}
	if _, err := tx.Exec(insSettingQuery, newPassphrase); err != nil {
		log.Printf("[ENGINE] [ERROR] RekeyDatabase: lỗi cập nhật master_passphrase: %v", err)
		return err
	}

	// 2. Accounts
	rows, err := tx.Query("SELECT id, email, name, avatar_url, credentials_json, token_json FROM accounts")
	if err != nil {
		log.Printf("[ENGINE] [ERROR] RekeyDatabase: lá»—i truy váº¥n accounts: %v", err)
		return err
	}
	type accRecord struct {
		id, email, name, avatar, creds, token string
	}
	var records []accRecord
	for rows.Next() {
		var a accRecord
		var e, n, av, c, t *string
		if err := rows.Scan(&a.id, &e, &n, &av, &c, &t); err != nil {
			rows.Close()
			return err
		}
		if e != nil { a.email = *e }
		if n != nil { a.name = *n }
		if av != nil { a.avatar = *av }
		if c != nil { a.creds = *c }
		if t != nil { a.token = *t }
		records = append(records, a)
	}
	rows.Close()

	for _, rec := range records {
		emailPlain, newEmail, err := processField("email", rec.id, rec.email)
		if err != nil {
			return err
		}
		namePlain, newName, err := processField("name", rec.id, rec.name)
		if err != nil {
			return err
		}
		_, newAvatar, err := processField("avatar_url", rec.id, rec.avatar)
		if err != nil {
			return err
		}
		_, newCreds, err := processField("credentials_json", rec.id, rec.creds)
		if err != nil {
			return err
		}
		_, newToken, err := processField("token_json", rec.id, rec.token)
		if err != nil {
			return err
		}

		emailHash := core.BlindIndexHash(newKey, emailPlain)
		nameHash := core.BlindIndexHash(newKey, namePlain)

		if _, err := tx.Exec("UPDATE accounts SET email = ?, name = ?, avatar_url = ?, credentials_json = ?, token_json = ?, email_hash = ?, name_hash = ? WHERE id = ?",
			newEmail, newName, newAvatar, newCreds, newToken, emailHash, nameHash, rec.id); err != nil {
			log.Printf("[ENGINE] [ERROR] RekeyDatabase: lá»—i cáº­p nháº­t accounts ID '%s': %v", rec.id, err)
			return err
		}
	}

	// 3. Users
	userRows, err := tx.Query("SELECT id, username, email, display_name, avatar_url FROM users")
	if err != nil {
		log.Printf("[ENGINE] [ERROR] RekeyDatabase: lá»—i truy váº¥n users: %v", err)
		return err
	}
	type userRecord struct {
		id, username, email, display, avatar string
	}
	var urecords []userRecord
	for userRows.Next() {
		var u userRecord
		var un, e, d, av *string
		if err := userRows.Scan(&u.id, &un, &e, &d, &av); err != nil {
			userRows.Close()
			return err
		}
		if un != nil { u.username = *un }
		if e != nil { u.email = *e }
		if d != nil { u.display = *d }
		if av != nil { u.avatar = *av }
		urecords = append(urecords, u)
	}
	userRows.Close()

	for _, u := range urecords {
		usernamePlain, newUsername, err := processField("username", u.id, u.username)
		if err != nil {
			return err
		}
		emailPlain, newEmail, err := processField("email", u.id, u.email)
		if err != nil {
			return err
		}
		_, newDisplay, err := processField("display_name", u.id, u.display)
		if err != nil {
			return err
		}
		_, newAvatar, err := processField("avatar_url", u.id, u.avatar)
		if err != nil {
			return err
		}

		usernameHash := core.BlindIndexHash(newKey, usernamePlain)
		emailHash := core.BlindIndexHash(newKey, emailPlain)

		if _, err := tx.Exec("UPDATE users SET username = ?, email = ?, display_name = ?, avatar_url = ?, username_hash = ?, email_hash = ? WHERE id = ?",
			newUsername, newEmail, newDisplay, newAvatar, usernameHash, emailHash, u.id); err != nil {
			log.Printf("[ENGINE] [ERROR] RekeyDatabase: lá»—i cáº­p nháº­t users ID '%s': %v", u.id, err)
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[ENGINE] [ERROR] RekeyDatabase: commit transaction tháº¥t báº¡i: %v", err)
		return err
	}

	log.Printf("[ENGINE] RekeyDatabase hoÃ n táº¥t thÃ nh cÃ´ng cho %d accounts vÃ  %d users", len(records), len(urecords))
	return nil
}

