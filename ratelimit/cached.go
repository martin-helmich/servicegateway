package ratelimit
import (
	"github.com/garyburd/redigo/redis"
	"sync"
	"time"
)


type RedisCachingRateThrottler struct {
	RedisSimpleRateThrottler

	updates chan string

	usedTokens map[string]int64
	usedTokensLock sync.RWMutex
}

func (t *RedisCachingRateThrottler) init() error {
	t.updates = make(chan string, 1024)

	go func() {
		flush := make(chan bool)
		lock := sync.Mutex{}
		usages := make(map[string]int64)
		updateCount := 0

		ticker := time.NewTicker(5 * time.Second)

		go func() {
			for {
				select {
				case <-flush:
				case <-ticker.C:
					lock.Lock()
					conn := t.redisPool.Get()

					for user, tokens := range usages {
						key := "RL_BUCKET_" + user

						conn.Send("MULTI")
						conn.Send("SET", key, 0, "EX", t.window.Seconds(), "NX")
						conn.Send("INCRBY", key, tokens)

						if val, err := redis.Values(conn.Do("EXEC")); err != nil {
							t.logger.Errorf("error while updating bucket level for user %s: %s", user, err)
						} else {
							t.usedTokens[user] = val[1].(int64)
						}

						delete(usages, user)
					}
					updateCount = 0

					conn.Close()
					lock.Unlock()
				}
			}
		}()

		for user := range t.updates {
			lock.Lock()
			u, ok := usages[user]
			if !ok {
				usages[user] = 1
			} else {
				usages[user] = u + 1
			}
			updateCount += 1
			lock.Unlock()

			if updateCount > 10 {
				flush <- true
			}
		}
	}()

	return nil
}

func (t *RedisCachingRateThrottler) getCachedBucketFill(user string) int64 {
	t.usedTokensLock.RLock()
	defer t.usedTokensLock.RUnlock()

	buck, ok := t.usedTokens[user]
	if !ok {
		return 0
	}
	return buck
}

func (t *RedisCachingRateThrottler) takeToken(user string) (int, int, error) {
	used := t.getCachedBucketFill(user)
	if used >= t.burstSize {
		return 0, int(t.burstSize), nil
	}

	t.updates <- user
	return int(t.burstSize - used - 1), int(t.burstSize), nil
}

//func (t *RedisCachingRateThrottler) syncRemoteBucket(user string) (int64, error) {
//	key := "RL_BUCKET_" + user
//	conn := t.redisPool.Get()
//	defer conn.Close()
//
//	conn.Send("MULTI")
//	conn.Send("SET", key, t.burstSize, "EX", t.window.Seconds(), "NX")
//	conn.Send("DECR", key)
//
//	if val, err := redis.Values(conn.Do("EXEC")); err != nil {
//		return 0, err
//	} else {
//		return int(val[1].(int64)), nil
//	}
//}
