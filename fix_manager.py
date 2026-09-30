import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go', 'r', encoding='utf-8') as f:
    content = f.read()

# Replace Service Account new service
new_sa_code = '''		// Tao custom HTTP client voi Connection Pooling cho SA
		transport := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		}
		saClient := &http.Client{Transport: transport}
		var errSrv error
		newSrv, errSrv = drive.NewService(context.Background(), option.WithCredentials(creds), option.WithHTTPClient(saClient))'''
content = content.replace('		var errSrv error\\n		newSrv, errSrv = drive.NewService(context.Background(), option.WithCredentials(creds))', new_sa_code)

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go', 'w', encoding='utf-8') as f:
    f.write(content)
