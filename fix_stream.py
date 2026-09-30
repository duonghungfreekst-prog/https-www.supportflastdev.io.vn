import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go', 'r', encoding='utf-8') as f:
    content = f.read()

# Increase maxEntries of cache from 256 to 1024
content = content.replace('maxEntries: 256,', 'maxEntries: 1024,')

# Increase NativeRangeBlockSize from 4MB to 16MB
content = content.replace('NativeRangeBlockSize int64 = 4 * 1024 * 1024 // 4 MB', 'NativeRangeBlockSize int64 = 16 * 1024 * 1024 // 16 MB')

# Increase prefetchSem capacity from 8 to 32
content = content.replace('prefetchSem = make(chan struct{}, 8)', 'prefetchSem = make(chan struct{}, 32)')

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go', 'w', encoding='utf-8') as f:
    f.write(content)
