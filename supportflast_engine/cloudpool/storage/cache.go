package storage

import (
	"fmt"
	"time"

	"supportflast_engine/cache"
	"supportflast_engine/cloudpool/models"
)

const (
	// TTL định nghĩa thời gian sống của các loại dữ liệu cố định trong L1 Cache (Rule 7.2)
	TTLVirtualFiles = 30 * time.Second
	TTLStats        = 30 * time.Second
	TTLAccounts     = 60 * time.Second
	TTLSettings     = 60 * time.Second

	// Cache Keys & Prefixes
	CachePrefixVFSFiles = "vfs:files:"
	CacheKeyStats       = "stats:storage"
	CacheKeyAccounts    = "accounts:list"
	CacheKeySettings    = "settings:all"
)

// getCache trả về đối tượng LRUCache nội bộ một cách an toàn và thread-safe
func (s *DB) getCache() *cache.LRUCache {
	if s == nil {
		return nil
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache == nil {
		s.cache = cache.NewLRUCache(cache.MaxEntriesLimit, cache.DefaultCleanupInterval)
	}
	return s.cache
}

// InvalidateVFSCache xóa toàn bộ cache danh sách file VFS
func (s *DB) InvalidateVFSCache() {
	if c := s.getCache(); c != nil {
		c.InvalidatePrefix(CachePrefixVFSFiles)
	}
}

// InvalidateAccountsCache xóa cache danh sách tài khoản và cache stats
func (s *DB) InvalidateAccountsCache() {
	if c := s.getCache(); c != nil {
		c.Invalidate(CacheKeyAccounts)
		c.Invalidate(CacheKeyStats)
	}
}

// InvalidateSettingsCache xóa cache cấu hình hệ thống và vfs cache
func (s *DB) InvalidateSettingsCache() {
	if c := s.getCache(); c != nil {
		c.Invalidate(CacheKeySettings)
		c.InvalidatePrefix(CachePrefixVFSFiles)
	}
}

// InvalidateStatsCache xóa cache thống kê dung lượng kho lưu trữ
func (s *DB) InvalidateStatsCache() {
	if c := s.getCache(); c != nil {
		c.Invalidate(CacheKeyStats)
	}
}

// ClearCache làm rỗng toàn bộ L1 Cache
func (s *DB) ClearCache() {
	if c := s.getCache(); c != nil {
		c.Clear()
	}
}

// ListVirtualFiles lấy danh sách file qua bộ đệm L1 Cache (TTL: 30s)
func (s *DB) ListVirtualFiles(userID, parentID string) ([]models.VirtualFile, error) {
	cacheKey := fmt.Sprintf("%s%s:%s", CachePrefixVFSFiles, userID, parentID)
	if c := s.getCache(); c != nil {
		if val, found := c.Get(cacheKey); found {
			if cachedList, ok := val.([]models.VirtualFile); ok {
				// Clone slice trước khi trả về để đảm bảo thread-safe và không làm sai lệch cache
				result := make([]models.VirtualFile, len(cachedList))
				copy(result, cachedList)
				return result, nil
			}
		}
	}

	list, err := s.listVirtualFilesFromDB(userID, parentID)
	if err != nil {
		return nil, err
	}

	if c := s.getCache(); c != nil {
		saveList := make([]models.VirtualFile, len(list))
		copy(saveList, list)
		c.Set(cacheKey, saveList, TTLVirtualFiles)
	}

	return list, nil
}

// GetStats lấy thống kê kho lưu trữ qua bộ đệm L1 Cache (TTL: 30s)
func (s *DB) GetStats() (*models.StorageStats, error) {
	if c := s.getCache(); c != nil {
		if val, found := c.Get(CacheKeyStats); found {
			if cachedStats, ok := val.(*models.StorageStats); ok && cachedStats != nil {
				cp := *cachedStats
				return &cp, nil
			}
		}
	}

	stats, err := s.getStatsFromDB()
	if err != nil {
		return nil, err
	}

	if c := s.getCache(); c != nil {
		cp := *stats
		c.Set(CacheKeyStats, &cp, TTLStats)
	}

	return stats, nil
}

// ListAccounts lấy danh sách tài khoản qua bộ đệm L1 Cache (TTL: 60s)
func (s *DB) ListAccounts() ([]models.Account, error) {
	if c := s.getCache(); c != nil {
		if val, found := c.Get(CacheKeyAccounts); found {
			if cachedList, ok := val.([]models.Account); ok {
				result := make([]models.Account, len(cachedList))
				copy(result, cachedList)
				return result, nil
			}
		}
	}

	list, err := s.listAccountsFromDB()
	if err != nil {
		return nil, err
	}

	if c := s.getCache(); c != nil {
		saveList := make([]models.Account, len(list))
		copy(saveList, list)
		c.Set(CacheKeyAccounts, saveList, TTLAccounts)
	}

	return list, nil
}

// GetSettings lấy cấu hình hệ thống qua bộ đệm L1 Cache (TTL: 60s)
func (s *DB) GetSettings() (*models.Settings, error) {
	if c := s.getCache(); c != nil {
		if val, found := c.Get(CacheKeySettings); found {
			if cachedSettings, ok := val.(*models.Settings); ok && cachedSettings != nil {
				cp := *cachedSettings
				return &cp, nil
			}
		}
	}

	settings, err := s.getSettingsFromDB()
	if err != nil {
		return nil, err
	}

	if c := s.getCache(); c != nil {
		cp := *settings
		c.Set(CacheKeySettings, &cp, TTLSettings)
	}

	return settings, nil
}
