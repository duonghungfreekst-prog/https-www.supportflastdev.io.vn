import mysql.connector
import os

try:
    conn = mysql.connector.connect(
        host="gateway01.ap-southeast-1.prod.aws.tidbcloud.com",
        port=4000,
        user="2KGt5QqixkveQPP.root",
        password="JIxWb1nGVINnKzap",
        database="supportflast",
        ssl_verify_cert=True,
        ssl_verify_identity=True
    )
    cursor = conn.cursor(dictionary=True)
    
    # Let's count them
    cursor.execute("SELECT COUNT(*) as count FROM virtual_files WHERE size_bytes = 1024")
    print("Files with size 1024 (1KB test files):", cursor.fetchone()['count'])
    
    cursor.execute("SELECT id, name FROM virtual_files WHERE size_bytes = 1024 OR name IN ('movie.mp4', 'missing.mp4', 'test_stream.mp4') OR name LIKE '%tar%'")
    test_files = cursor.fetchall()
    print("Found test files:", len(test_files))
    
    # Delete them
    if test_files:
        ids_to_delete = [f"'{f['id']}'" for f in test_files]
        query = f"DELETE FROM virtual_files WHERE id IN ({','.join(ids_to_delete)})"
        cursor.execute(query)
        conn.commit()
        print("Deleted!")
        
        # Also clean up file_chunks and public_shares just in case
        cursor.execute(f"DELETE FROM file_chunks WHERE file_id IN ({','.join(ids_to_delete)})")
        cursor.execute(f"DELETE FROM public_shares WHERE file_id IN ({','.join(ids_to_delete)})")
        conn.commit()
        print("Cleaned chunks and shares too!")

    conn.close()
except Exception as e:
    print("Error:", e)
