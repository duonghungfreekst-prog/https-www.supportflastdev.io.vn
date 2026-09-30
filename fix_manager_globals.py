import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# Add a global variable declaration of sharedTransport
new_global = '''
var sharedTransport = &http.Transport{
	MaxIdleConns:          500,
	MaxIdleConnsPerHost:   500,
	ForceAttemptHTTP2:     true,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	DisableCompression:    false,
	WriteBufferSize:       128 * 1024,
	ReadBufferSize:        128 * 1024,
}
'''

if "var sharedTransport" not in code:
    insert_pos = code.find('type Manager struct')
    code = code[:insert_pos] + new_global + '\n' + code[insert_pos:]

# Remove any inner declarations
code = re.sub(r'sharedTransport := &http\.Transport\{.*?\}', '', code, flags=re.DOTALL)

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)

