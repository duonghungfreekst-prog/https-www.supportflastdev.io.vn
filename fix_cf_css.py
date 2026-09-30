import re

with open(r'f:\supportflast.dev\supportflast_ui\index.html', 'r', encoding='utf-8') as f:
    content = f.read()

content = content.replace('align-items: center !important;', 'align-items: flex-start !important;')

with open(r'f:\supportflast.dev\supportflast_ui\index.html', 'w', encoding='utf-8') as f:
    f.write(content)
