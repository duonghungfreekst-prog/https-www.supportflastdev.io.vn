import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go', 'r', encoding='utf-8') as f:
    lines = f.readlines()

in_stream = False
for i, line in enumerate(lines):
    if 'func (s *Server) handleStreamFile' in line:
        in_stream = True
    if in_stream and 'func (s *Server)' in line and 'handleStreamFile' not in line:
        in_stream = False
    if in_stream and 'w.Header().Set' in line:
        print(f"{i}: {line.strip()}")
