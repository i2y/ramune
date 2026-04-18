// Copyright 2026 The Ramune Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !libc.membrk && !libc.memgrind

package libc // import "modernc.org/libc"

import (
	"sync"

	"modernc.org/memory"
)

// Per-TLS allocators (TLS.allocator) each manage a disjoint set of mmap'd
// pages. Callers that pass a nil TLS (e.g. libc.CString) hit the package-level
// `allocator`. Inside modernc.org/quickjs and modernc.org/sqlite, a string
// allocated with nil TLS is later freed with a valid TLS — a cross-allocator
// free that corrupts page bookkeeping because the per-TLS allocator does not
// own the page it is being asked to free.
//
// pageOwner tracks which *memory.Allocator owns each mmap'd page base so that
// Xfree / Xrealloc can route the operation to the correct allocator. The map
// is sharded by page address to avoid a single mutex recreating the old
// allocMu bottleneck.
const (
	pageOwnerShift        = 16 // matches modernc.org/memory.pageSizeLog
	pageOwnerMask  uintptr = (1 << pageOwnerShift) - 1
	pageOwnerShards       = 64
)

type pageOwnerShard struct {
	mu sync.RWMutex
	m  map[uintptr]*memory.Allocator
	_  [40]byte // pad to a cache line to avoid false sharing between shards
}

// Use a slice (initialized inline) rather than an array-with-init-func, because
// libc.go's package init calls Xcalloc before any sibling init func runs.
// Slice literal sidesteps the init ordering issue: the backing storage is
// constructed by the runtime during var evaluation, which happens before any
// init func in the package.
var pageOwners = func() *[pageOwnerShards]pageOwnerShard {
	a := &[pageOwnerShards]pageOwnerShard{}
	for i := range a {
		a[i].m = make(map[uintptr]*memory.Allocator)
	}
	return a
}()

func pageOwnerShardFor(pg uintptr) *pageOwnerShard {
	// Multiplicative hash — page addresses come from mmap in clusters, so
	// naive bit-masking would hot-spot a few shards.
	h := uint64(pg) * 0x9E3779B97F4A7C15
	return &pageOwners[h>>(64-6)] // 6 bits = 64 shards
}

// registerPageOwner records that the page containing p belongs to allocator a.
// Optimized for the common case where the page is already registered to the
// same allocator (read-only fast path). Safe to call concurrently.
func registerPageOwner(p uintptr, a *memory.Allocator) {
	if p == 0 {
		return
	}
	pg := p &^ pageOwnerMask
	sh := pageOwnerShardFor(pg)
	sh.mu.RLock()
	cur := sh.m[pg]
	sh.mu.RUnlock()
	if cur == a {
		return
	}
	sh.mu.Lock()
	sh.m[pg] = a
	sh.mu.Unlock()
}

// lookupPageOwner returns the allocator that owns the page containing p, or
// nil if the pointer does not belong to any tracked allocator (e.g. a pointer
// allocated before the libc fork started tracking).
func lookupPageOwner(p uintptr) *memory.Allocator {
	if p == 0 {
		return nil
	}
	pg := p &^ pageOwnerMask
	sh := pageOwnerShardFor(pg)
	sh.mu.RLock()
	a := sh.m[pg]
	sh.mu.RUnlock()
	return a
}
