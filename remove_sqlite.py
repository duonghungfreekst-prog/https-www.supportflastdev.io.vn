import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\storage\db.go', 'r', encoding='utf-8') as f:
    content = f.read()

# Remove migrateSQLite
content = re.sub(r'func \(s \*DB\) migrateSQLite\(\) error \{.*?(?=func \(s \*DB\)|$)', '', content, flags=re.DOTALL)

# In migrate, remove s.migrateSQLite() fallback
content = re.sub(r'if s\.IsMySQLOrTiDB\(\) \{\s*return s\.migrateTiDB\(\)\s*\}\s*return s\.migrateSQLite\(\)', 'return s.migrateTiDB()', content)

# Remove SyncToLocalSQLiteCacheIfMissing
content = re.sub(r'// SyncToLocalSQLiteCacheIfMissing.*?func \(s \*DB\) SyncToLocalSQLiteCacheIfMissing\(\) error \{.*?(?=func |$)', '', content, flags=re.DOTALL)

# In checkAndBootstrap, remove go s.SyncToLocalSQLiteCacheIfMissing()
content = content.replace('go s.SyncToLocalSQLiteCacheIfMissing()', '')

# Modify s.GetTables
new_get_tables = '''func (s *DB) GetTables() ([]string, error) {
	if s.IsMySQLOrTiDB() {
		rows, err := s.db.Query("SHOW TABLES")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var tables []string
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err == nil {
				tables = append(tables, t)
			}
		}
		return tables, nil
	}
	return nil, fmt.Errorf("Only MySQL/TiDB is supported")
}'''

content = re.sub(r'func \(s \*DB\) GetTables\(\) \(\[\]string, error\) \{.*?(?=func |$)', new_get_tables + '\n\n', content, flags=re.DOTALL)

# Modify BackupDatabase
new_backup = '''func (s *DB) BackupDatabase(destPath string) error {
	return fmt.Errorf("BackupDatabase using VACUUM is only for SQLite. Use TiDB Backup & Restore (BR) or mysqldump.")
}'''
content = re.sub(r'func \(s \*DB\) BackupDatabase\(destPath string\) error \{.*?(?=func |$)', new_backup + '\n\n', content, flags=re.DOTALL)

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\storage\db.go', 'w', encoding='utf-8') as f:
    f.write(content)
