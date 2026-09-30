import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\api\handlers.go', 'r', encoding='utf-8') as f:
    lines = f.readlines()

in_func = False
for i, line in enumerate(lines):
    if 'func (s *Server) handleCreatePublicShare' in line:
        in_func = True
    if in_func and 'func (s *Server)' in line and 'handleCreatePublicShare' not in line:
        in_func = False
        break
    if in_func:
        print(f"{i}: {line.strip().encode('utf-8', 'ignore').decode('cp1252', 'ignore')}")
