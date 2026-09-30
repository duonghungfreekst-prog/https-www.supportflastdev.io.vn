import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# First add the shared transport logic
shared_transport_pattern = r'sharedTransport := &http.Transport\{(.*?)\}'
new_shared_transport = '''sharedTransport := &http.Transport{
		MaxIdleConns:          500,
		MaxIdleConnsPerHost:   500,
		ForceAttemptHTTP2:     true,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
		WriteBufferSize:       128 * 1024,
		ReadBufferSize:        128 * 1024,
	}'''

code = re.sub(shared_transport_pattern, new_shared_transport, code, flags=re.DOTALL)

# Now fix the Service Account client to use this transport
sa_pattern = r'newSrv, errSrv = drive\.NewService\(context\.Background\(\), option\.WithCredentials\(creds\)\)'
sa_replacement = '''saTransport := &oauth2.Transport{
			Source: creds.TokenSource,
			Base:   sharedTransport,
		}
		saClient := &http.Client{Transport: saTransport}
		newSrv, errSrv = drive.NewService(context.Background(), option.WithHTTPClient(saClient))'''

code = re.sub(sa_pattern, sa_replacement, code)

# Make sure "golang.org/x/oauth2" is imported if it isn't
if '"golang.org/x/oauth2"' not in code:
    code = code.replace('"context"', '"context"\n\t"golang.org/x/oauth2"')

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)

