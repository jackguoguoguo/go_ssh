package store

// GetSettings 返回设置副本，零值字段用默认值补齐。
// 注意：Store 已有 Settings 字段，因此读取器不能同名。
func (s *Store) GetSettings() Settings {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return s.Settings.withDefaults()
}

// UpdateSettings 通过回调修改设置。修改不会自动落盘，调用方需自行调用 Save。
func (s *Store) UpdateSettings(fn func(*Settings)) {
	if fn == nil {
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	fn(&s.Settings)
}
