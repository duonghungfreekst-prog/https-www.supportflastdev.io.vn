import io

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# Replace actual newlines inside the Printf string literal with \\n
# Just look for the broken strings
code = code.replace('%v\\n",', '%v\\\\n",')
code = code.replace('%v\n", s.file.ID', '%v\\n", s.file.ID')

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
