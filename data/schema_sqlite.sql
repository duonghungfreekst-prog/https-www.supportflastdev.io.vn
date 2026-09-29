-- =========================================================
-- SUPPORTFLAST DATABASE SCHEMA (SQLite / Standard SQL)
-- Nền tảng: supportflastdev.io.vn
-- =========================================================

PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

BEGIN TRANSACTION;
CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    name TEXT,
    key_hash TEXT UNIQUE,
    prefix TEXT,
    status TEXT DEFAULT 'active',
    permissions TEXT,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE apps (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    platform TEXT,
    category TEXT,
    desc TEXT,
    file_name TEXT,
    size_bytes INTEGER,
    size_formatted TEXT,
    sha256 TEXT,
    author TEXT,
    downloads INTEGER DEFAULT 0,
    status TEXT DEFAULT 'published',
    published_at TEXT,
    download_url TEXT,
    video_url TEXT,
    guide TEXT,
    user_id TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE TABLE audit_logs (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    action TEXT NOT NULL,
    ip_address TEXT,
    user_agent TEXT,
    details TEXT,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE TABLE reviews (
    id TEXT PRIMARY KEY,
    app_id TEXT,
    user_id TEXT,
    author_name TEXT NOT NULL,
    author_role TEXT,
    stars INTEGER NOT NULL,
    text TEXT NOT NULL,
    status TEXT DEFAULT 'approved',
    created_at TEXT,
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
);
CREATE TABLE system_releases (
    version TEXT PRIMARY KEY,
    title TEXT,
    date TEXT,
    build_hash TEXT,
    notes TEXT,
);
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    avatar TEXT,
    created_at TEXT,
    updated_at TEXT,
    last_login TEXT
);
INSERT INTO "users" VALUES('usr-admin-001','admin','admin@supportflastdev.io.vn','$2a$12$Uw5F331gwniPZO3bb0462e25Rov8PDZO50aGiKoWO411rgAmMv/N.','Quản Trị Viên Hệ Thống','admin','/avatars/admin.png','2026-09-24T04:15:48Z','2026-09-24T14:15:42+07:00','2026-09-24T14:15:42+07:00');
INSERT INTO "users" VALUES('usr-217003020','quocviet_dev','quocviet@supportflastdev.io.vn','$2a$12$7T0UHhEfC4GNVrtpcxaOl.mW.ppKczgkjNm24DZPgwKgCEHn1R902','Quốc Việt Developer','developer','💻','2026-09-24T08:39:29+07:00','2026-09-24T11:21:00+07:00','2026-09-24T08:39:29+07:00');
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_role ON users(role);
CREATE INDEX idx_users_created_at ON users(created_at);
CREATE INDEX idx_apps_user_id ON apps(user_id);
CREATE INDEX idx_apps_status ON apps(status);
CREATE INDEX idx_apps_category ON apps(category);
CREATE INDEX idx_apps_platform ON apps(platform);
CREATE INDEX idx_apps_published_at ON apps(published_at);
CREATE INDEX idx_apps_downloads ON apps(downloads DESC);
CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX idx_api_keys_prefix ON api_keys(prefix);
CREATE INDEX idx_api_keys_status ON api_keys(status);
CREATE INDEX idx_reviews_app_id ON reviews(app_id);
CREATE INDEX idx_reviews_user_id ON reviews(user_id);
CREATE INDEX idx_reviews_status ON reviews(status);
CREATE INDEX idx_reviews_created_at ON reviews(created_at);
CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at DESC);
CREATE INDEX idx_system_releases_date ON system_releases(date DESC);
COMMIT;
