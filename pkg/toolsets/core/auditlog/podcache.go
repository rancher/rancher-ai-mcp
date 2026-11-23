package auditlog

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sync"
	"time"
)

const (
	// podCacheTTL is how long the names of the Rancher pods with an audit log sidecar are remembered. Pods only
	// change on a rollout, restart or scale, and a search streams their whole logs, which is far more expensive
	// than listing them. The entry is dropped as soon as reading a pod fails, which is what happens when a
	// remembered pod is replaced, so the TTL only bounds how long a pod added next to the remembered ones, e.g.
	// by scaling up, goes unnoticed. Each Rancher pod logs the requests it served, so such a pod is missing
	// from the results until the entry expires.
	podCacheTTL = 5 * time.Minute
	// podCacheMaxEntries bounds the number of cached callers.
	podCacheMaxEntries = 64
)

// podCache remembers the names of the pods whose audit log is searched. Entries are keyed by caller, because
// the pods a caller can list depend on their permissions, which a shared entry would bypass.
type podCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]podCacheEntry
}

type podCacheEntry struct {
	pods    []string
	expires time.Time
}

func newPodCache(ttl time.Duration) *podCache {
	return &podCache{ttl: ttl, entries: map[string]podCacheEntry{}}
}

// podCacheKey returns the cache key for a caller's token and cluster. The token is hashed so that it is not
// kept in memory longer than the request needs it.
func podCacheKey(token, cluster string) string {
	sum := sha256.Sum256([]byte(token + "\x00" + cluster))
	return hex.EncodeToString(sum[:])
}

// get returns the cached pod names, if there is an unexpired entry.
func (c *podCache) get(key string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !time.Now().Before(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return slices.Clone(e.pods), true
}

// set caches the pod names. It does not cache an empty list, so that enabling the audit log sidecar is noticed
// by the next search.
func (c *podCache) set(key string, pods []string) {
	if len(pods) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
	for k := range c.entries {
		if len(c.entries) < podCacheMaxEntries {
			break
		}
		if k != key {
			delete(c.entries, k)
		}
	}
	c.entries[key] = podCacheEntry{pods: slices.Clone(pods), expires: now.Add(c.ttl)}
}

// delete drops an entry, e.g. when a cached pod can no longer be read.
func (c *podCache) delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
