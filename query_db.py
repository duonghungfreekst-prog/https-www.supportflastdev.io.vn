import mysql.connector

try:
    conn = mysql.connector.connect(
        host="127.0.0.1",
        port=4000,
        user="root",
        password="",
        database="supportflast"
    )
    cursor = conn.cursor(dictionary=True)
    cursor.execute("SELECT id, name, size_bytes FROM virtual_files LIMIT 20;")
    rows = cursor.fetchall()
    for row in rows:
        print(f"{row['id']} | {row['name']} | {row['size_bytes']}")
except Exception as e:
    print("Error:", e)
