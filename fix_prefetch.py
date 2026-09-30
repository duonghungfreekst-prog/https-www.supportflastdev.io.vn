import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go', 'r', encoding='utf-8') as f:
    content = f.read()

content = content.replace('var prefetchSem = make(chan struct{}, 8)', 'var prefetchSem = make(chan struct{}, 2)')

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go', 'w', encoding='utf-8') as f:
    f.write(content)
