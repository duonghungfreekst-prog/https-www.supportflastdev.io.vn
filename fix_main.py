import io
import re

path = r'f:\supportflast.dev\supportflast_engine\main.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

code = code.replace("		ReadHeaderTimeout: 30 * time.Second,  // Bảo vệ Slowloris trên headers\n", "")
code = code.replace("		ReadHeaderTimeout: 30 * time.Second,\n", "")

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
