import pymysql

conn = pymysql.connect(
    host='gateway01.ap-southeast-1.prod.aws.tidbcloud.com', 
    port=4000, 
    user='2KGt5QqixkveQPP.root', 
    password='JIxWb1nGVINnKzap', 
    database='supportflast',
    ssl_verify_cert=True,
    ssl_verify_identity=True
)
c = conn.cursor()

c.execute('''
    SELECT id FROM virtual_files 
    WHERE id LIKE 'file_test_%' 
       OR id LIKE 'vfile_test_%'
       OR id IN ('file_native_video_test', 'file_missing_all_chunks', 'file_stream_test', 'file_test_video_20mb', 'file_test_audio_5mb')
''')
fake_files = [row[0] for row in c.fetchall()]

if fake_files:
    format_strings = ','.join(['%s'] * len(fake_files))
    c.execute('DELETE FROM file_chunks WHERE file_id IN (%s)' % format_strings, tuple(fake_files))
    print(f"Deleted {c.rowcount} fake chunks")

    c.execute('DELETE FROM virtual_files WHERE id IN (%s)' % format_strings, tuple(fake_files))
    print(f"Deleted {c.rowcount} fake files")

c.execute('''
    DELETE FROM accounts
    WHERE id IN ('acc_test', 'acc_worker_1', 'acc_worker_2', 'acc_drive_native', 'acc_banned_1', 'acc_banned_2', 'acc_banned_3')
''')
print(f"Deleted {c.rowcount} fake accounts")

conn.commit()
conn.close()
