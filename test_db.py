import mysql.connector

try:
    conn = mysql.connector.connect(
        host="gateway01.ap-southeast-1.prod.aws.tidbcloud.com",
        port=4000,
        user="supportflast.1169641738",
        password="f297be7dffc79f8263eb87a1779ddb2c011e",
        database="supportflast",
        ssl_verify_cert=True
    )
    cursor = conn.cursor(dictionary=True)
    cursor.execute("SELECT id, username, role FROM cloudpool_users")
    rows = cursor.fetchall()
    print("cloudpool_users:")
    for row in rows:
        print(row)
        
    cursor.execute("SELECT id, username, role FROM users")
    rows = cursor.fetchall()
    print("users:")
    for row in rows:
        print(row)
except Exception as e:
    print(f"Error: {e}")
