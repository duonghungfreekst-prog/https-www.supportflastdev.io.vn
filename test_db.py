import mysql.connector

dsn_parts = {
    'host': 'gateway01.ap-southeast-1.prod.aws.tidbcloud.com',
    'port': 4000,
    'user': '1169641738.supportflast',
    'password': 'f297be7dffc79f8263eb87a1779ddb2c011e',
    'database': 'supportflast',
    'ssl_ca': '',
    'ssl_verify_cert': False
}

try:
    conn = mysql.connector.connect(**dsn_parts)
    cursor = conn.cursor()
    cursor.execute("SELECT id, username, role FROM cloudpool_users")
    rows = cursor.fetchall()
    print("cloudpool_users:", rows)
    
    cursor.execute("SELECT id, username, role FROM users")
    rows = cursor.fetchall()
    print("users:", rows)
except Exception as e:
    print("Error:", e)
