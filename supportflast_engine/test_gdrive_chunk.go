package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"supportflast_engine/cloudpool/gdrive"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"
	"time"
)

func main() {
	dbPath := filepath.Join("..", "data", "cloudpool_metadata.db")
	if _, err := os.Stat(dbPath); err != nil {
		dbPath = filepath.Join("data", "cloudpool_metadata.db")
	}

	db, err := storage.NewDB(dbPath)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		return
	}
	defer db.Close()

	gd := gdrive.NewManager(db)
	v := vfs.NewVFS(db, gd)

	fileID := "file_abc7420a-f85b-4ead-a1d8-15b57914a34b"
	ctx := context.Background()

	fmt.Println("1. Calling NewFileStreamer...")
	streamer, err := v.NewFileStreamer(ctx, fileID)
	if err != nil {
		fmt.Printf("NewFileStreamer err: %v\n", err)
		return
	}
	defer streamer.Close()

	fmt.Printf("2. Streamer created: size=%d, modTime=%v\n", streamer.Size(), streamer.ModTime())

	buf := make([]byte, 1024)
	fmt.Println("3. Calling streamer.Read...")
	start := time.Now()
	n, err := streamer.Read(buf)
	fmt.Printf("4. Read 1024 bytes in %v: n=%d, err=%v\n", time.Since(start), n, err)
	if n > 0 {
		fmt.Printf("5. First 16 bytes: %x\n", buf[:min(n, 16)])
	}
}
