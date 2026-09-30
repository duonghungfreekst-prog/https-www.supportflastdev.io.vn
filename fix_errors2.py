import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

code = code.replace("fs.lastPrefetchedIdx.Store(-1)", "fs.lastPrefetchChunk.Store(-1)")
code = code.replace("fs.lastPrefetchedBlock.Store(-1)", "fs.lastPrefetchBlock.Store(-1)")
code = code.replace("fs.lastPrefetchedBlk.Store(-1)", "fs.lastPrefetchBlock.Store(-1)")

code = code.replace("if s.lastPrefetchedBlock.Load() != nextBlockIdx && blockEnd+1 < chunkTotalSize {", "if s.lastPrefetchBlock.Load() != blockIdx && blockEnd+1 < chunkTotalSize {")
code = code.replace("s.triggerRangePrefetch(chunk, nextBlockIdx, chunkTotalSize)", "s.triggerRangePrefetchAhead(chunk, blockIdx, chunkTotalSize)")

code = code.replace("if s.lastPrefetchedIdx.Load() != int32(nextChunkIdx) && nextChunkIdx < len(s.chunks) {", "if s.lastPrefetchChunk.Load() != int32(chunkIdx) && chunkIdx+1 < len(s.chunks) {")
code = code.replace("s.triggerPrefetch(nextChunkIdx)", "s.triggerPrefetchAhead(chunkIdx)")

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
