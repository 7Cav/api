package cache

import (
	"encoding/json"
	"github.com/7cav/api/datastores"
	"github.com/7cav/api/xenforo"
	"github.com/redis/go-redis/v9"
	"time"
)

var monitoredTables = map[string]struct{}{
	"xf_nf_rosters_user_award":     {},
	"xf_nf_rosters_position_group": {},
	"xf_nf_rosters_position":       {},
	"xf_nf_rosters_user":           {},
	"xf_nf_rosters_rank":           {},
	"xf_nf_rosters_service_record": {},
	"xf_nf_rosters_record_type":    {},
	"xf_nf_rosters":                {},
	"xf_user":                      {},
	"xf_user_connected_account":    {},
	"xf_post":                      {},
}

func CacheManager(cache *RedisCache, ds datastores.Datastore) {
	Info.Println("Starting cache manager")
	for {
		updates, err := ds.GetTableUpdates()
		if err != nil {
			Error.Println("error fetching table updates: ", err)
		}

		serializedUpdates, err := json.Marshal(updates)
		if err != nil {
			Error.Println("error serializing updates: ", err)
		}

		existingCache, err := cache.Get("table_updates")
		if err != nil && err != redis.Nil {
			Error.Println("error fetching existing table updates: ", err)
		}

		var invalidate bool
		var existingUpdates []xenforo.TableInfo
		if existingCache == nil {
			Info.Println("No existing table updates found, caching")
			invalidate = true
		} else {
			if err := json.Unmarshal(existingCache, &existingUpdates); err != nil {
				Error.Println("error deserializing existing cache: ", err)
			}
		}

		if len(updates) != len(existingUpdates) {
			invalidate = true
		} else {
			existingMap := make(map[string]string)
			for _, update := range existingUpdates {

				if _, shouldMonitor := monitoredTables[update.TableName]; shouldMonitor {
					existingMap[update.TableName] = update.UpdateTime
				}
			}

			for _, update := range updates {

				if _, shouldMonitor := monitoredTables[update.TableName]; shouldMonitor {
					if cachedTime, exists := existingMap[update.TableName]; !exists || cachedTime != update.UpdateTime {
						Info.Printf("Table %s update detected, invalidating cache", update.TableName)
						invalidate = true
						break
					}
				}
			}
		}

		if invalidate == true {
			cache.flush()
			if err := cache.Set("table_updates", serializedUpdates); err != nil {
				Error.Println("error setting cache: ", err)
			}
		} else {
			Info.Println("Cached table updates are up to date, not invalidating")
		}
		time.Sleep(time.Minute)
	}
}
