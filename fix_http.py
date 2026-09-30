import re

# Fix handlers.go
handlers_path = r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go'
with open(handlers_path, 'r', encoding='utf-8') as f:
    handlers = f.read()

handlers = handlers.replace(
    'ReadTimeout:  30 * time.Minute, // Large file upload support\n\t\tWriteTimeout: 30 * time.Minute, // Large file stream support',
    'ReadHeaderTimeout: 20 * time.Second,\n\t\tReadTimeout:       60 * time.Minute,\n\t\tWriteTimeout:      0,\n\t\tIdleTimeout:       120 * time.Second,\n\t\tMaxHeaderBytes:    2 << 20,'
)
with open(handlers_path, 'w', encoding='utf-8') as f:
    f.write(handlers)


# Fix main.go
main_path = r'f:\supportflast.dev\supportflast_engine\main.go'
with open(main_path, 'r', encoding='utf-8') as f:
    main_go = f.read()

main_go = re.sub(
    r'ReadTimeout:\s*15 \* time\.Minute,.*?WriteTimeout:\s*30 \* time\.Minute,.*?IdleTimeout:\s*120 \* time\.Second,',
    'ReadHeaderTimeout: 20 * time.Second,\n\t\tReadTimeout:       60 * time.Minute,\n\t\tWriteTimeout:      0,\n\t\tIdleTimeout:       120 * time.Second,\n\t\tMaxHeaderBytes:    2 << 20,',
    main_go, flags=re.DOTALL
)

with open(main_path, 'w', encoding='utf-8') as f:
    f.write(main_go)
