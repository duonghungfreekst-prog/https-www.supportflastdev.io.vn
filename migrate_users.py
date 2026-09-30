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

c_sqlite.execute(f"SELECT * FROM users")
rows = c_sqlite.fetchall()
if rows:
    columns = [desc[0] for desc in c_sqlite.description]
    col_str = ', '.join(columns)
    placeholders = ', '.join(['%s'] * len(columns))
    
    insert_query = f"INSERT IGNORE INTO cloudpool_users ({col_str}) VALUES ({placeholders})"
    c_tidb.executemany(insert_query, rows)
    conn_tidb.commit()
    print(f'Migrated {len(rows)} rows to cloudpool_users.')

conn_tidb.close()
conn_sqlite.close()
