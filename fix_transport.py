import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# I will find the sharedTransport declaration in the oauth block and move it before the if block
pattern = r'sharedTransport := &http\.Transport\{.*?\}'
match = re.search(pattern, code, re.DOTALL)
if match:
    transport_decl = match.group(0)
    # Remove it from the current location
    code = code.replace(transport_decl, '')
    
    # Insert it before if acc.AuthType == "service_account"
    insert_point = code.find('if acc.AuthType == "service_account"')
    code = code[:insert_point] + transport_decl + '\n\n\t' + code[insert_point:]
    
    with io.open(path, 'w', encoding='utf-8') as f:
        f.write(code)

