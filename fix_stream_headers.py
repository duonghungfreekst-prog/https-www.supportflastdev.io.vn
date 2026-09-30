import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go', 'r', encoding='utf-8') as f:
    content = f.read()

old_headers = '''	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", formatContentDisposition("inline", targetFileName))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	http.ServeContent(w, r, targetFileName, streamer.ModTime(), streamer)
}

func (s *Server) handlePublicShareDownload'''

new_headers = '''	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", formatContentDisposition("inline", targetFileName))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	
	if models.IsMediaStreamable(mimeType) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	} else {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
	}
	
	http.ServeContent(w, r, targetFileName, streamer.ModTime(), streamer)
}

func (s *Server) handlePublicShareDownload'''

content = content.replace(old_headers, new_headers)

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go', 'w', encoding='utf-8') as f:
    f.write(content)
