import sqlite3
import os

dbs = [
    r'D:\Temp\test_bootstrap_1790734917484423300\data\cloudpool_metadata.db',
    r'D:\Temp\test_bootstrap_1790735203523806400\data\cloudpool_metadata.db',
    r'D:\Temp\test_bootstrap_1790736262250039800\data\cloudpool_metadata.db',
    r'D:\Temp\test_bootstrap_1790737072964167600\data\cloudpool_metadata.db',
    r'F:\tools luu tr?\cloudpool\cloudpool_engine\data\cloudpool_metadata.db',
    r'F:\tools luu tr?\cloudpool\data\cloudpool_metadata.db'
]

for db in dbs:
    if os.path.exists(db):
        try:
            conn = sqlite3.connect(db)
            count = conn.execute('SELECT COUNT(*) FROM public_shares').fetchone()[0]
            print(f'{db}: {count} shares')
            conn.close()
        except Exception as e:
            print(f'{db}: error {e}')
    else:
        print(f'{db}: not found')
