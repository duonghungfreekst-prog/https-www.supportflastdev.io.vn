import io

path1 = r'f:\supportflast.dev\supportflast_engine\database\tidb.go'
with io.open(path1, 'r', encoding='utf-8') as f:
    content = f.read()

content = content.replace('DefaultTiDBMaxIdleConns    = 100', 'DefaultTiDBMaxIdleConns    = 200')
content = content.replace('DefaultTiDBConnMaxLifetime = 30 * time.Minute', 'DefaultTiDBConnMaxLifetime = 5 * time.Minute')

if "interpolateParams=true" not in content:
    content = content.replace('"%s:%s@tcp(%s:%d)/%s?tls=%s&parseTime=true"', '"%s:%s@tcp(%s:%d)/%s?tls=%s&parseTime=true&interpolateParams=true"')

with io.open(path1, 'w', encoding='utf-8') as f:
    f.write(content)

path2 = r'f:\supportflast.dev\supportflast_engine\cloudpool\storage\db.go'
with io.open(path2, 'r', encoding='utf-8') as f:
    content2 = f.read()

if "interpolateParams=true" not in content2:
    content2 = content2.replace('"%s:%s@tcp(%s:%d)/%s?tls=%s&parseTime=true"', '"%s:%s@tcp(%s:%d)/%s?tls=%s&parseTime=true&interpolateParams=true"')

new_indexes = """
    INDEX idx_vfiles_trash_admin (is_deleted, deleted_at),
    INDEX idx_vfiles_trash_user (user_id, is_deleted, deleted_at),
    INDEX idx_vfiles_size_admin (is_deleted, is_dir, size_bytes),
    INDEX idx_vfiles_size_user (user_id, is_deleted, is_dir, size_bytes),
    INDEX idx_vfiles_parent_name (parent_id, name, is_dir)
"""
if "idx_vfiles_trash_admin" not in content2:
    # Need to insert these indexes at the end of the virtual_files DDL.
    # Look for INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name)
    content2 = content2.replace('INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name)', 'INDEX idx_vfiles_user_parent_del_dir_name (user_id, parent_id, is_deleted, is_dir, name),' + new_indexes)

if "idx_public_shares_file" not in content2:
    # Look for public_shares DDL
    # INDEX idx_public_shares (id, is_active)
    content2 = content2.replace('INDEX idx_public_shares (id, is_active)', 'INDEX idx_public_shares (id, is_active),\n    INDEX idx_public_shares_file (file_id, is_active)')

with io.open(path2, 'w', encoding='utf-8') as f:
    f.write(content2)

