# -*- coding: utf-8 -*-
import io
import re

path = r'f:\supportflast.dev\supportflast_engine\cloudpool\vfs\stream.go'
with io.open(path, 'r', encoding='utf-8') as f:
    code = f.read()

# 1. Semaphore
code = code.replace("prefetchSem = make(chan struct{}, 8)", "prefetchSem = make(chan struct{}, 32)")

# 2. Fix the CAS bug in triggerRangePrefetch
bug1 = """	if !s.lastPrefetchedBlk.CompareAndSwap(old, int32(nextBlockIdx)) {
		return
	}"""
code = code.replace(bug1, "")

# 3. Fix the CAS bug in triggerPrefetch
bug2 = """	if !s.lastPrefetchedIdx.CompareAndSwap(old, int32(nextChunkIdx)) {
		return
	}"""
code = code.replace(bug2, "")

# 4. Fix semaphore drop bug
sem_bug = """		case prefetchSem <- struct{}{}:
			// OK, tiếp tục
		default:
			// Đầy luồng tải ngầm, bỏ qua
			return
		}"""
sem_fix = """		case prefetchSem <- struct{}{}:
		default:
			return
		}"""
code = code.replace(sem_bug, sem_fix)

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(code)
