import re

with open(r'f:\supportflast.dev\nginx_supportflastdev.io.vn.conf', 'r', encoding='utf-8') as f:
    content = f.read()

new_location = '''    # Streaming Endpoints (Video/Audio)
    location ~ ^/api/(files|public/share)/stream {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host System.Management.Automation.Internal.Host.InternalHost;
        proxy_set_header X-Real-IP ;
        proxy_set_header X-Forwarded-For ;
        proxy_set_header X-Forwarded-Proto ;
        
        proxy_buffering off;
        proxy_connect_timeout 30s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;
    }

    # API Endpoints'''

content = content.replace('    # API Endpoints', new_location)

with open(r'f:\supportflast.dev\nginx_supportflastdev.io.vn.conf', 'w', encoding='utf-8') as f:
    f.write(content)
