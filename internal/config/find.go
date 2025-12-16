package config

import "strings"

func FindPool(cfg *AppConfig, poolID string) (PoolConfig, bool) {
	if cfg == nil {
		return PoolConfig{}, false
	}
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return PoolConfig{}, false
	}
	for _, pool := range cfg.Pools {
		if pool.ID == poolID {
			return pool, true
		}
	}
	return PoolConfig{}, false
}
