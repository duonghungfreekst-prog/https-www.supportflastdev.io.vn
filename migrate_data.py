import sqlite3
import pymysql

# Connect to SQLite
sqlite_db = r'D:\Temp\test_bootstrap_1790737072964167600\data\cloudpool_metadata.db'
conn_sqlite = sqlite3.connect(sqlite_db)
c_sqlite = conn_sqlite.cursor()

# Connect to TiDB
conn_tidb = pymysql.connect(
    host='gateway01.ap-southeast-1.prod.aws.tidbcloud.com',
    port=4000,
    user='2KGt5QqixkveQPP.root',
    password='JIxWb1nGVINnKzap',
    database='supportflast',
    ssl_verify_cert=True,
    ssl_verify_identity=True
)
c_tidb = conn_tidb.cursor()

tables = [('virtual_files', 'virtual_files'), ('file_chunks', 'file_chunks'), ('public_shares', 'public_shares'), ('cloudpool_users', 'users')]

for sqlite_table, tidb_table in tables:
    try:
        print(f'Migrating {sqlite_table} to {tidb_table}...')
        c_sqlite.execute(f"SELECT * FROM {sqlite_table}")
        rows = c_sqlite.fetchall()
        if not rows:
            print("No data.")
            continue
            
        columns = [desc[0] for desc in c_sqlite.description]
        col_str = ', '.join(columns)
        placeholders = ', '.join(['%s'] * len(columns))
        
        insert_query = f"INSERT IGNORE INTO {tidb_table} ({col_str}) VALUES ({placeholders})"
        c_tidb.executemany(insert_query, rows)
        conn_tidb.commit()
        print(f'Migrated {len(rows)} rows to {tidb_table}.')
    except Exception as e:
        print(f"Error on {sqlite_table}: {e}")

conn_tidb.close()
conn_sqlite.close()
print('Migration complete.')
