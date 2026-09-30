import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go'
with open(path, 'r', encoding='utf-8') as f:
    content = f.read()

content = content.replace('MaxIdleConns:          100,', 'MaxIdleConns:          500,')
content = content.replace('MaxIdleConnsPerHost:   100,', 'MaxIdleConnsPerHost:   500,')

with open(path, 'w', encoding='utf-8') as f:
    f.write(content)
