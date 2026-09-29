package webdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"supportflast_engine/cloudpool/core"
	"supportflast_engine/cloudpool/models"
	"supportflast_engine/cloudpool/storage"
	"supportflast_engine/cloudpool/vfs"

	netwebdav "golang.org/x/net/webdav"
)

type contextKey string

const UserIDContextKey contextKey = "webdav_user_id"

// ContextWithUserID returns a new context with userID attached.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDContextKey, userID)
}

// GetUserIDFromContext retrieves the userID from context.
func GetUserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if uid, ok := ctx.Value(UserIDContextKey).(string); ok && uid != "" {
		return uid
	}
	return ""
}

type CloudPoolFS struct {
	vfs    *vfs.VFS
	db     *storage.DB
	userID string
}

func NewCloudPoolFS(vfs *vfs.VFS, db *storage.DB) *CloudPoolFS {
	return &CloudPoolFS{
		vfs: vfs,
		db:  db,
	}
}

func NewCloudPoolFSWithUser(vfs *vfs.VFS, db *storage.DB, userID string) *CloudPoolFS {
	return &CloudPoolFS{
		vfs:    vfs,
		db:     db,
		userID: userID,
	}
}

func (fs *CloudPoolFS) resolveUserID(ctx context.Context) string {
	if ctx != nil {
		if uid := GetUserIDFromContext(ctx); uid != "" {
			return uid
		}
	}
	if fs.userID != "" {
		return fs.userID
	}
	return "user_guest"
}

// Mkdir implements webdav.FileSystem
func (fs *CloudPoolFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	cleanName := path.Clean("/" + strings.TrimPrefix(name, "/"))
	dir := path.Dir(cleanName)
	base := path.Base(cleanName)

	parentFile, err := fs.vfs.GetFileByPath(dir)
	if err != nil {
		return os.ErrNotExist
	}

	userID := fs.resolveUserID(ctx)
	_, err = fs.vfs.Mkdir(userID, parentFile.ID, base)
	return err
}

// OpenFile implements webdav.FileSystem
func (fs *CloudPoolFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (netwebdav.File, error) {
	cleanName := path.Clean("/" + strings.TrimPrefix(name, "/"))
	userID := fs.resolveUserID(ctx)

	// 1. Directory Open
	if cleanName == "/" {
		rootFile, err := fs.db.GetVirtualFile("root")
		if err != nil {
			return nil, os.ErrNotExist
		}
		return &WebDAVVirtualFile{
			fs:     fs,
			ctx:    ctx,
			userID: userID,
			vfile:  rootFile,
			isDir:  true,
		}, nil
	}

	// 2. Check if file exists
	vfile, err := fs.vfs.GetFileByPath(cleanName)
	if err == nil {
		if vfile.IsDir {
			return &WebDAVVirtualFile{
				fs:     fs,
				ctx:    ctx,
				userID: userID,
				vfile:  vfile,
				isDir:  true,
			}, nil
		}

		// Read existing file
		streamer, err := fs.vfs.NewFileStreamer(ctx, vfile.ID)
		if err != nil {
			return nil, err
		}

		return &WebDAVVirtualFile{
			fs:       fs,
			ctx:      ctx,
			userID:   userID,
			vfile:    vfile,
			streamer: streamer,
			isDir:    false,
		}, nil
	}

	// 3. Create new file if flag has O_CREATE or O_WRONLY
	if flag&os.O_CREATE != 0 || flag&os.O_WRONLY != 0 {
		parentDir := path.Dir(cleanName)
		fileName := path.Base(cleanName)

		parentFile, err := fs.vfs.GetFileByPath(parentDir)
		if err != nil {
			return nil, os.ErrNotExist
		}

		return &WebDAVVirtualFile{
			fs:          fs,
			ctx:         ctx,
			userID:      userID,
			parentID:    parentFile.ID,
			name:        fileName,
			isWriting:   true,
			writeBuffer: new(bytes.Buffer),
		}, nil
	}

	return nil, os.ErrNotExist
}

// RemoveAll implements webdav.FileSystem
func (fs *CloudPoolFS) RemoveAll(ctx context.Context, name string) error {
	cleanName := path.Clean("/" + strings.TrimPrefix(name, "/"))
	if cleanName == "/" {
		return errors.New("cannot remove root directory")
	}

	vfile, err := fs.vfs.GetFileByPath(cleanName)
	if err != nil {
		return os.ErrNotExist
	}

	userID := fs.resolveUserID(ctx)
	if userID != "user_admin" && userID != "admin" {
		if vfile.UserID != "" && vfile.UserID != userID {
			return os.ErrPermission
		}
	}

	return fs.vfs.DeleteFileOrFolder(ctx, vfile.ID)
}

// Rename implements webdav.FileSystem
func (fs *CloudPoolFS) Rename(ctx context.Context, oldName, newName string) error {
	cleanOld := path.Clean("/" + strings.TrimPrefix(oldName, "/"))
	cleanNew := path.Clean("/" + strings.TrimPrefix(newName, "/"))

	vfile, err := fs.vfs.GetFileByPath(cleanOld)
	if err != nil {
		return os.ErrNotExist
	}

	userID := fs.resolveUserID(ctx)
	if userID != "user_admin" && userID != "admin" {
		if vfile.UserID != "" && vfile.UserID != userID {
			return os.ErrPermission
		}
	}

	return fs.vfs.RenameFileOrFolder(vfile.ID, path.Base(cleanNew))
}

// Stat implements webdav.FileSystem
func (fs *CloudPoolFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	cleanName := path.Clean("/" + strings.TrimPrefix(name, "/"))
	vfile, err := fs.vfs.GetFileByPath(cleanName)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return &virtualFileInfo{vfile: vfile}, nil
}

// WebDAVVirtualFile implements netwebdav.File
type WebDAVVirtualFile struct {
	fs          *CloudPoolFS
	ctx         context.Context
	userID      string
	vfile       *models.VirtualFile
	streamer    *vfs.FileStreamer
	isDir       bool
	dirChildren []models.VirtualFile
	dirOffset   int
	isWriting   bool
	writeBuffer *bytes.Buffer
	parentID    string
	name        string
	mu          sync.Mutex
}

func (f *WebDAVVirtualFile) getUserID() string {
	if f.userID != "" {
		return f.userID
	}
	if f.ctx != nil {
		if uid := GetUserIDFromContext(f.ctx); uid != "" {
			return uid
		}
	}
	if f.fs != nil {
		return f.fs.resolveUserID(f.ctx)
	}
	return "user_guest"
}

func (f *WebDAVVirtualFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.isWriting && f.writeBuffer != nil {
		// Save uploaded file into CloudPool VFS with resolved user ID
		_, err := f.fs.vfs.UploadFile(f.ctx, f.getUserID(), f.parentID, f.name, f.writeBuffer, int64(f.writeBuffer.Len()))
		f.writeBuffer = nil
		return err
	}

	if f.streamer != nil {
		return f.streamer.Close()
	}
	return nil
}

func (f *WebDAVVirtualFile) Read(p []byte) (n int, err error) {
	if f.isDir {
		return 0, errors.New("cannot read a directory as a stream")
	}
	if f.streamer == nil {
		return 0, io.EOF
	}
	return f.streamer.Read(p)
}

func (f *WebDAVVirtualFile) Seek(offset int64, whence int) (int64, error) {
	if f.streamer == nil {
		return 0, nil
	}
	return f.streamer.Seek(offset, whence)
}

func (f *WebDAVVirtualFile) Write(p []byte) (n int, err error) {
	if !f.isWriting || f.writeBuffer == nil {
		return 0, errors.New("file not opened for writing")
	}
	return f.writeBuffer.Write(p)
}

func (f *WebDAVVirtualFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.isDir {
		return nil, errors.New("not a directory")
	}

	if f.dirChildren == nil {
		children, err := f.fs.db.ListVirtualFiles(f.getUserID(), f.vfile.ID)
		if err != nil {
			return nil, err
		}
		f.dirChildren = children
	}

	if f.dirOffset >= len(f.dirChildren) {
		if count > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}

	limit := len(f.dirChildren)
	if count > 0 && f.dirOffset+count < limit {
		limit = f.dirOffset + count
	}

	var infos []os.FileInfo
	for i := f.dirOffset; i < limit; i++ {
		infos = append(infos, &virtualFileInfo{vfile: &f.dirChildren[i]})
	}
	f.dirOffset = limit

	return infos, nil
}

func (f *WebDAVVirtualFile) Stat() (os.FileInfo, error) {
	if f.vfile != nil {
		return &virtualFileInfo{vfile: f.vfile}, nil
	}
	return &virtualFileInfo{
		vfile: &models.VirtualFile{
			Name:      f.name,
			SizeBytes: int64(f.writeBuffer.Len()),
			UpdatedAt: time.Now(),
		},
	}, nil
}

// virtualFileInfo implements os.FileInfo
type virtualFileInfo struct {
	vfile *models.VirtualFile
}

func (fi *virtualFileInfo) Name() string       { return fi.vfile.Name }
func (fi *virtualFileInfo) Size() int64        { return fi.vfile.SizeBytes }
func (fi *virtualFileInfo) Mode() os.FileMode {
	if fi.vfile.IsDir {
		return os.ModeDir | 0755
	}
	return 0644
}
func (fi *virtualFileInfo) ModTime() time.Time { return fi.vfile.UpdatedAt }
func (fi *virtualFileInfo) IsDir() bool        { return fi.vfile.IsDir }
func (fi *virtualFileInfo) Sys() interface{}   { return nil }

// CreateWebDAVHandler returns an HTTP handler for WebDAV with authentication
func CreateWebDAVHandler(vfs *vfs.VFS, db *storage.DB) http.Handler {
	lockSystem := netwebdav.NewMemLS()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := db.GetSettings()
		if err != nil || !settings.WebDAVEnabled {
			http.Error(w, "WebDAV service is disabled", http.StatusForbidden)
			return
		}

		resolvedUserID := ""
		authenticated := false

		// Basic Auth check
		user, pass, ok := r.BasicAuth()
		if ok {
			// 1. Check against WebDAV settings credentials
			if settings.WebDAVUsername != "" && settings.WebDAVPassword != "" {
				if core.ConstantTimeCompare(user, settings.WebDAVUsername) && core.ConstantTimeCompare(pass, settings.WebDAVPassword) {
					authenticated = true
					if u, err := db.GetUserByUsername(user); err == nil && u != nil {
						resolvedUserID = u.ID
					} else if adminUser, err := db.GetUserByUsername("admin"); err == nil && adminUser != nil {
						resolvedUserID = adminUser.ID
					} else {
						resolvedUserID = "user_admin"
					}
				}
			}

			// 2. If not authenticated via WebDAV settings, check user database
			if !authenticated {
				if u, err := db.GetUserByUsername(user); err == nil && u != nil {
					if core.CheckPasswordHashBcrypt(pass, u.PasswordHash) {
						authenticated = true
						resolvedUserID = u.ID
					}
				}
			}
		}

		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Basic realm="CloudPool WebDAV"`)
			http.Error(w, "Unauthorized: Cần xác thực tài khoản để truy cập WebDAV", http.StatusUnauthorized)
			return
		}

		ctx := ContextWithUserID(r.Context(), resolvedUserID)
		reqWithUser := r.WithContext(ctx)

		davFS := NewCloudPoolFSWithUser(vfs, db, resolvedUserID)
		davHandler := &netwebdav.Handler{
			Prefix:     "/webdav",
			FileSystem: davFS,
			LockSystem: lockSystem,
		}

		davHandler.ServeHTTP(w, reqWithUser)
	})
}

