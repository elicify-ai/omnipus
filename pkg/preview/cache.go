// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"container/list"
	"sync"
)

// pageCacheBytes is the rendered-page cache budget per gateway (AD-6).
const pageCacheBytes = 64 << 20

// cacheKey is AD-9's key: content digest, page, render settings and the
// renderer build + protocol version. It is looked up only after the calling
// request authorized and read the source.
type cacheKey struct {
	source   [32]byte
	page     int
	settings string
	renderer string
}

type cacheEntry struct {
	key  cacheKey
	page Page
}

// pageCache is a byte-bounded least-recently-used cache of rendered pages.
type pageCache struct {
	mu    sync.Mutex
	limit int
	used  int
	order *list.List
	items map[cacheKey]*list.Element
}

func newPageCache(limit int) *pageCache {
	return &pageCache{limit: limit, order: list.New(), items: map[cacheKey]*list.Element{}}
}

func (c *pageCache) get(k cacheKey) (Page, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return Page{}, false
	}
	c.order.MoveToFront(el)
	e, _ := el.Value.(*cacheEntry)
	return e.page, true
}

func (c *pageCache) put(k cacheKey, p Page) {
	size := len(p.PNG)
	if size > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		e, _ := el.Value.(*cacheEntry)
		c.used -= len(e.page.PNG)
		e.page = p
		c.used += size
		c.order.MoveToFront(el)
	} else {
		c.items[k] = c.order.PushFront(&cacheEntry{key: k, page: p})
		c.used += size
	}
	for c.used > c.limit {
		back := c.order.Back()
		if back == nil {
			break
		}
		e, _ := back.Value.(*cacheEntry)
		c.order.Remove(back)
		delete(c.items, e.key)
		c.used -= len(e.page.PNG)
	}
}
