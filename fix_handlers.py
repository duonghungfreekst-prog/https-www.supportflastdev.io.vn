import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go', 'r', encoding='utf-8') as f:
    content = f.read()

content = content.replace('w.Header().Set("Content-Type", mimeType)\\n\\tw.Header().Set("Content-Disposition", formatContentDisposition("inline", targetFileName))', 'w.Header().Set("Content-Type", mimeType)\\n\\tw.Header().Set("Cache-Control", "public, max-age=3600")\\n\\tw.Header().Set("Content-Disposition", formatContentDisposition("inline", targetFileName))')

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go', 'w', encoding='utf-8') as f:
    f.write(content)
