import re

with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go', 'r', encoding='utf-8') as f:
    content = f.read()

# Fix DownloadChunk
chunk_old = '''// DownloadChunk ti nTi dung chunk t Google Drive
func (m *Manager) DownloadChunk(ctx context.Context, accountID, gdriveFileID string) ([]byte, error) {
	srv, _, err := m.GetService(context.Background(), accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		dlCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		resp, err := srv.Files.Get(gdriveFileID).Context(dlCtx).Download()
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				data, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				cancel()
				if readErr == nil {
					return data, nil
				}'''

chunk_new = '''// DownloadChunk tai noi dung chunk tu Google Drive
func (m *Manager) DownloadChunk(ctx context.Context, accountID, gdriveFileID string) ([]byte, error) {
	srv, _, err := m.GetService(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drive service: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		resp, err := srv.Files.Get(gdriveFileID).Context(dlCtx).Download()
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				var data []byte
				var readErr error
				if resp.ContentLength > 0 {
					data = make([]byte, resp.ContentLength)
					_, readErr = io.ReadFull(resp.Body, data)
				} else {
					data, readErr = io.ReadAll(resp.Body)
				}
				_ = resp.Body.Close()
				cancel()
				if readErr == nil || readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
					return data, nil
				}'''

# Since there are encoding issues with Vietnamese characters, let's use regex that avoids them.
# I'll just replace the specific sections.

content = content.replace('srv, _, err := m.GetService(context.Background(), accountID)', 'srv, _, err := m.GetService(ctx, accountID)')
content = content.replace('dlCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)', 'dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)')
content = content.replace('dlCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)', 'dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)')

# For DownloadChunk
content = content.replace('data, readErr := io.ReadAll(resp.Body)', '''var data []byte
				var readErr error
				if resp.ContentLength > 0 {
					data = make([]byte, resp.ContentLength)
					_, readErr = io.ReadFull(resp.Body, data)
				} else {
					data, readErr = io.ReadAll(resp.Body)
				}''')
				
# For DownloadRange (return io.ReadAll(resp.Body))
content = content.replace('return io.ReadAll(resp.Body)', '''if resp.ContentLength > 0 {
		data := make([]byte, resp.ContentLength)
		_, err := io.ReadFull(resp.Body, data)
		return data, err
	}
	return io.ReadAll(resp.Body)''')


with open(r'f:\supportflast.dev\supportflast_engine\cloudpool\gdrive\manager.go', 'w', encoding='utf-8') as f:
    f.write(content)
