import io

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

code = code.replace("s.triggerRangePrefetch(chunk, blockIdx+1, chunkTotalSize)", "s.triggerRangePrefetchAhead(chunk, blockIdx, chunkTotalSize)")
code = code.replace("int(s.lastPrefetchedIdx.Load()) != chunkIdx+1", "int(s.lastPrefetchChunk.Load()) != chunkIdx+1")
code = code.replace("s.triggerPrefetch(chunkIdx + 1)", "s.triggerPrefetchAhead(chunkIdx)")

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
