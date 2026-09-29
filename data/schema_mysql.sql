-- =====================================================================
-- DATABASE SCHEMA CHO HOSTING (MySQL 4.0 / 5.x / 8.x & MariaDB)
-- Nền tảng: supportflastdev.io.vn
-- Kiến trúc: SupportFlast Database Schema & Seed Data
-- =====================================================================

SET FOREIGN_KEY_CHECKS = 0;
SET SQL_MODE = 'NO_AUTO_VALUE_ON_ZERO';

-- ---------------------------------------------------------------------
-- 1. BẢNG users: Quản lý người dùng, phân quyền và thông tin đăng nhập
-- ---------------------------------------------------------------------
DROP TABLE IF EXISTS `users`;
CREATE TABLE `users` (
  `id` varchar(64) NOT NULL,
  `username` varchar(100) NOT NULL,
  `email` varchar(191) NOT NULL,
  `password_hash` varchar(255) NOT NULL,
  `display_name` varchar(150) DEFAULT NULL,
  `role` varchar(50) NOT NULL DEFAULT 'user',
  `avatar` varchar(255) DEFAULT NULL,
  `created_at` varchar(50) DEFAULT NULL,
  `updated_at` varchar(50) DEFAULT NULL,
  `last_login` varchar(50) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_users_username` (`username`),
  UNIQUE KEY `idx_users_email` (`email`),
  KEY `idx_users_role` (`role`),
  KEY `idx_users_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------
-- 2. BẢNG apps: Kho ứng dụng, gói phần mềm và bài viết công bố tính năng
-- ---------------------------------------------------------------------
DROP TABLE IF EXISTS `apps`;
CREATE TABLE `apps` (
  `id` varchar(64) NOT NULL,
  `name` varchar(255) NOT NULL,
  `version` varchar(50) NOT NULL,
  `platform` varchar(150) DEFAULT NULL,
  `category` varchar(150) DEFAULT NULL,
  `desc` text DEFAULT NULL,
  `file_name` varchar(255) DEFAULT NULL,
  `size_bytes` bigint(20) DEFAULT 0,
  `size_formatted` varchar(50) DEFAULT NULL,
  `sha256` varchar(100) DEFAULT NULL,
  `author` varchar(150) DEFAULT NULL,
  `downloads` int(11) DEFAULT 0,
  `status` varchar(50) DEFAULT 'published',
  `published_at` varchar(50) DEFAULT NULL,
  `download_url` varchar(255) DEFAULT NULL,
  `video_url` varchar(255) DEFAULT NULL,
  `guide` longtext DEFAULT NULL,
  `user_id` varchar(64) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_apps_user_id` (`user_id`),
  KEY `idx_apps_status` (`status`),
  KEY `idx_apps_category` (`category`),
  KEY `idx_apps_platform` (`platform`),
  KEY `idx_apps_published_at` (`published_at`),
  KEY `idx_apps_downloads` (`downloads`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------
-- 3. BẢNG api_keys: Khóa API cho CI/CD và tự động hóa xuất bản từ máy dev
-- ---------------------------------------------------------------------
DROP TABLE IF EXISTS `api_keys`;
CREATE TABLE `api_keys` (
  `id` varchar(64) NOT NULL,
  `user_id` varchar(64) DEFAULT NULL,
  `name` varchar(150) DEFAULT NULL,
  `key_hash` varchar(191) DEFAULT NULL,
  `prefix` varchar(50) DEFAULT NULL,
  `status` varchar(50) DEFAULT 'active',
  `permissions` text DEFAULT NULL,
  `created_at` varchar(50) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_api_keys_key_hash` (`key_hash`),
  KEY `idx_api_keys_user_id` (`user_id`),
  KEY `idx_api_keys_prefix` (`prefix`),
  KEY `idx_api_keys_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------
-- 4. BẢNG reviews: Đánh giá và xếp hạng ứng dụng từ cộng đồng
-- ---------------------------------------------------------------------
DROP TABLE IF EXISTS `reviews`;
CREATE TABLE `reviews` (
  `id` varchar(64) NOT NULL,
  `app_id` varchar(64) DEFAULT NULL,
  `user_id` varchar(64) DEFAULT NULL,
  `author_name` varchar(150) NOT NULL,
  `author_role` varchar(100) DEFAULT NULL,
  `stars` int(11) NOT NULL,
  `text` text NOT NULL,
  `status` varchar(50) DEFAULT 'approved',
  `created_at` varchar(50) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_reviews_app_id` (`app_id`),
  KEY `idx_reviews_user_id` (`user_id`),
  KEY `idx_reviews_status` (`status`),
  KEY `idx_reviews_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------
-- 5. BẢNG audit_logs: Nhật ký kiểm toán an ninh và truy vết thao tác
-- ---------------------------------------------------------------------
DROP TABLE IF EXISTS `audit_logs`;
CREATE TABLE `audit_logs` (
  `id` varchar(64) NOT NULL,
  `user_id` varchar(64) DEFAULT NULL,
  `action` varchar(100) NOT NULL,
  `ip_address` varchar(100) DEFAULT NULL,
  `user_agent` text DEFAULT NULL,
  `details` text DEFAULT NULL,
  `created_at` varchar(50) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_audit_logs_user_id` (`user_id`),
  KEY `idx_audit_logs_action` (`action`),
  KEY `idx_audit_logs_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------
-- 6. BẢNG system_releases: Phiên bản cập nhật và changelog của hệ thống web
-- ---------------------------------------------------------------------
DROP TABLE IF EXISTS `system_releases`;
CREATE TABLE `system_releases` (
  `version` varchar(50) NOT NULL,
  `title` varchar(255) DEFAULT NULL,
  `date` varchar(50) DEFAULT NULL,
  `build_hash` varchar(100) DEFAULT NULL,
  `notes` text DEFAULT NULL,
  `published_by` varchar(150) DEFAULT NULL,
  PRIMARY KEY (`version`),
  KEY `idx_system_releases_date` (`date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- =====================================================================
-- DỮ LIỆU THỰC TẾ (INSERT DATA)
-- =====================================================================


-- Dữ liệu bảng `users` (2 bản ghi)
INSERT INTO `users` (`id`, `username`, `email`, `password_hash`, `display_name`, `role`, `avatar`, `created_at`, `updated_at`, `last_login`) VALUES
('usr-admin-001', 'admin', 'admin@supportflastdev.io.vn', '$2a$12$Uw5F331gwniPZO3bb0462e25Rov8PDZO50aGiKoWO411rgAmMv/N.', 'Quản Trị Viên Hệ Thống', 'admin', '/avatars/admin.png', '2026-09-24T04:15:48Z', '2026-09-24T14:15:42+07:00', '2026-09-24T14:15:42+07:00'),
('usr-217003020', 'quocviet_dev', 'quocviet@supportflastdev.io.vn', '$2a$12$7T0UHhEfC4GNVrtpcxaOl.mW.ppKczgkjNm24DZPgwKgCEHn1R902', 'Quốc Việt Developer', 'developer', '💻', '2026-09-24T08:39:29+07:00', '2026-09-24T11:21:00+07:00', '2026-09-24T08:39:29+07:00');

SET FOREIGN_KEY_CHECKS = 1;
