import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# I will replace the constant declarations
const_pattern = r'const \(\s*// NativeRangeBlockSize.*?\)'
# Actually, I already removed NativeRangeBlockSize in the SLRU patch!
# Let me just insert MaxConcurrentPrefetch and MaxPrefetchAhead after the imports
if "MaxConcurrentPrefetch" not in code:
    import_end = code.find(')') + 1
    new_const = '''
const (
	MaxConcurrentPrefetch = 32
	MaxPrefetchAhead = 2
)
'''
    code = code[:import_end] + new_const + code[import_end:]

# Replace flightMu and prefetchSem
old_vars = """var (
	flightMu    sync.Mutex
	inFlight    = make(map[string]*chunkFlight)
	prefetchSem = make(chan struct{}, 8)
)"""
new_vars = """var (
	flightMu    sync.Mutex
	inFlight    = make(map[string]*chunkFlight)
	prefetchSem = make(chan struct{}, MaxConcurrentPrefetch)
)"""
code = code.replace(old_vars, new_vars)

# FileStreamer additions
old_fs = """type FileStreamer struct {
	ctx               context.Context
	cancel            context.CancelFunc
	vfs               *VFS
	file              *models.VirtualFile
	chunks            []models.FileChunk
	encKey            [32]byte
	offset            int64
	chunkSize         int64
	mu                sync.Mutex
	lastPrefetchedIdx atomic.Int32
	lastPrefetchedBlk atomic.Int64
}"""
new_fs = """type FileStreamer struct {
	ctx               context.Context
	cancel            context.CancelFunc
	vfs               *VFS
	file              *models.VirtualFile
	chunks            []models.FileChunk
	encKey            [32]byte
	offset            int64
	chunkSize         int64
	mu                sync.Mutex
	lastPrefetchChunk atomic.Int32
	lastPrefetchBlock atomic.Int64
}"""
code = code.replace(old_fs, new_fs)

# Replace init values
code = code.replace("fs.lastPrefetchedIdx.Store(-1)", "fs.lastPrefetchChunk.Store(-1)")
code = code.replace("fs.lastPrefetchedBlk.Store(-1)", "fs.lastPrefetchBlock.Store(-1)")

# Replace the body of triggerRangePrefetch
range_prefetch_pattern = re.compile(r'func \(s \*FileStreamer\) triggerRangePrefetch\(chunk models\.FileChunk, nextBlockIdx int64, chunkTotalSize int64\) \{.*?^\}', re.DOTALL | re.MULTILINE)
new_range_prefetch = """func (s *FileStreamer) triggerRangePrefetchAhead(chunk models.FileChunk, currentBlockIdx int64, chunkTotalSize int64) {
	allQueued := true

	for step := int64(1); step <= MaxPrefetchAhead; step++ {
		targetBlockIdx := currentBlockIdx + step
		blockStart := targetBlockIdx * NativeRangeBlockSize
		if chunkTotalSize > 0 && blockStart >= chunkTotalSize {
			break
		}
		blockEnd := blockStart + NativeRangeBlockSize - 1
		if chunkTotalSize > 0 && blockEnd >= chunkTotalSize {
			blockEnd = chunkTotalSize - 1
		}

		rangeKey := fmt.Sprintf("range_%s_%s_%d", chunk.AccountID, chunk.GDriveFileID, blockStart)

		if globalChunkCache.Contains(rangeKey) {
			continue
		}

		flightMu.Lock()
		if _, running := inFlight[rangeKey]; running {
			flightMu.Unlock()
			continue
		}
		flightMu.Unlock()

		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		if mem.Alloc > 400*1024*1024 { 
			allQueued = false
			break
		}

		select {
		case prefetchSem <- struct{}{}:
			go func(bIdx, bStart, bEnd int64) {
				defer func() {
					<-prefetchSem
					if r := recover(); r != nil {
					}
				}()
				if s.ctx != nil && s.ctx.Err() != nil {
					return
				}
				_, err := s.fetchNativeRangeBlock(chunk, bIdx, bStart, bEnd)
				if err != nil && is404(err) {
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 in prefetch (file %s, range %d-%d): %v\\n", s.file.ID, bStart, bEnd, err)
				}
			}(targetBlockIdx, blockStart, blockEnd)
		default:
			allQueued = false
			break
		}
	}

	if allQueued {
		s.lastPrefetchBlock.Store(currentBlockIdx)
	}
}"""
code = range_prefetch_pattern.sub(new_range_prefetch, code)

# Replace triggerPrefetch
prefetch_pattern = re.compile(r'func \(s \*FileStreamer\) triggerPrefetch\(nextChunkIdx int\) \{.*?^\}', re.DOTALL | re.MULTILINE)
new_prefetch = """func (s *FileStreamer) triggerPrefetchAhead(currentChunkIdx int) {
	allQueued := true

	for step := 1; step <= MaxPrefetchAhead; step++ {
		targetIdx := currentChunkIdx + step
		if targetIdx >= len(s.chunks) {
			break
		}

		targetChunk := s.chunks[targetIdx]
		cacheKey := fmt.Sprintf("chunk_%s_%s", targetChunk.AccountID, targetChunk.GDriveFileID)

		if globalChunkCache.Contains(cacheKey) {
			continue
		}

		flightMu.Lock()
		if _, running := inFlight[cacheKey]; running {
			flightMu.Unlock()
			continue
		}
		flightMu.Unlock()

		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		if mem.Alloc > 400*1024*1024 {
			allQueued = false
			break
		}

		select {
		case prefetchSem <- struct{}{}:
			go func(chk models.FileChunk, idx int) {
				defer func() {
					<-prefetchSem
					if r := recover(); r != nil {
					}
				}()
				if s.ctx != nil && s.ctx.Err() != nil {
					return
				}
				_, err := s.fetchAndDecryptChunk(chk)
				if err != nil && is404(err) {
					fmt.Printf("[STREAMER ERROR] Chunk missing on Drive: 404 in prefetch (file %s, chunk %d): %v\\n", s.file.ID, idx, err)
				}
			}(targetChunk, targetIdx)
		default:
			allQueued = false
			break
		}
	}

	if allQueued {
		s.lastPrefetchChunk.Store(int32(currentChunkIdx))
	}
}"""
code = prefetch_pattern.sub(new_prefetch, code)

# Update readInternal calls
code = code.replace("if s.lastPrefetchedBlk.Load() != nextBlockIdx && blockEnd+1 < chunkTotalSize {", "if s.lastPrefetchBlock.Load() != blockIdx && blockEnd+1 < chunkTotalSize {")
code = code.replace("s.triggerRangePrefetch(chunk, nextBlockIdx, chunkTotalSize)", "s.triggerRangePrefetchAhead(chunk, blockIdx, chunkTotalSize)")

code = code.replace("if s.lastPrefetchedIdx.Load() != int32(nextChunkIdx) && nextChunkIdx < len(s.chunks) {", "if s.lastPrefetchChunk.Load() != int32(chunkIdx) && chunkIdx+1 < len(s.chunks) {")
code = code.replace("s.triggerPrefetch(nextChunkIdx)", "s.triggerPrefetchAhead(chunkIdx)")

# Import runtime
if '"runtime"' not in code:
    code = code.replace('"context"', '"context"\n\t"runtime"')

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
