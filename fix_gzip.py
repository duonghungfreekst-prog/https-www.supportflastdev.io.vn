import re

with open(r'f:\supportflast.dev\supportflast_engine\internal\middleware\gzip.go', 'r', encoding='utf-8') as f:
    content = f.read()

pattern = re.compile(r'func GzipMiddleware\(next http\.Handler\) http\.Handler \{\s+return http\.HandlerFunc\(func\(w http\.ResponseWriter, r \*http\.Request\) \{')
replacement = '''func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/files/stream") || strings.HasPrefix(r.URL.Path, "/api/public/share/stream") {
			next.ServeHTTP(w, r)
			return
		}'''
content = pattern.sub(replacement, content)

with open(r'f:\supportflast.dev\supportflast_engine\internal\middleware\gzip.go', 'w', encoding='utf-8') as f:
    f.write(content)
