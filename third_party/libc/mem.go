// Copyright 2021 The Libc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !libc.membrk && !libc.memgrind && !(linux && (amd64 || arm64 || loong64 || ppc64le || s390x || riscv64 || 386 || arm))

package libc // import "modernc.org/libc"

import (
	"modernc.org/libc/errno"
	"modernc.org/libc/sys/types"
	"modernc.org/memory"
)

const memgrind = false

var (
	allocator memory.Allocator
)

// void *malloc(size_t size);
func Xmalloc(t *TLS, n types.Size_t) uintptr {
	if __ccgo_strace {
		trc("t=%v n=%v, (%v:)", t, n, origin(2))
	}
	if n == 0 {
		// malloc(0) should return unique pointers
		// (often expected and gnulib replaces malloc if malloc(0) returns 0)
		n = 1
	}

	var p uintptr
	var err error
	var owner *memory.Allocator
	if t != nil && t.allocator != nil {
		p, err = t.allocator.UintptrMalloc(int(n))
		owner = t.allocator
	} else {
		allocMu.Lock()
		p, err = allocator.UintptrMalloc(int(n))
		allocMu.Unlock()
		owner = &allocator
	}
	if err != nil {
		if t != nil {
			t.setErrno(errno.ENOMEM)
		}
		return 0
	}
	registerPageOwner(p, owner)
	return p
}

// void *calloc(size_t nmemb, size_t size);
func Xcalloc(t *TLS, n, size types.Size_t) uintptr {
	if __ccgo_strace {
		trc("t=%v n=%v size=%v, (%v:)", t, n, size, origin(2))
	}
	rq := int(n * size)
	if rq == 0 {
		rq = 1
	}

	var p uintptr
	var err error
	var owner *memory.Allocator
	if t != nil && t.allocator != nil {
		p, err = t.allocator.UintptrCalloc(rq)
		owner = t.allocator
	} else {
		allocMu.Lock()
		p, err = allocator.UintptrCalloc(rq)
		allocMu.Unlock()
		owner = &allocator
	}
	if err != nil {
		if t != nil {
			t.setErrno(errno.ENOMEM)
		}
		return 0
	}
	registerPageOwner(p, owner)
	return p
}

// void *realloc(void *ptr, size_t size);
func Xrealloc(t *TLS, ptr uintptr, size types.Size_t) uintptr {
	if __ccgo_strace {
		trc("t=%v ptr=%v size=%v, (%v:)", t, ptr, size, origin(2))
	}

	// Route the realloc to whichever allocator currently owns ptr, so
	// bookkeeping stays consistent. If ptr == 0 this acts like Xmalloc and
	// we pick the TLS allocator (or fall back to global).
	var owner *memory.Allocator
	if ptr != 0 {
		owner = lookupPageOwner(ptr)
	}
	if owner == nil {
		if t != nil && t.allocator != nil {
			owner = t.allocator
		} else {
			owner = &allocator
		}
	}

	var p uintptr
	var err error
	if owner == &allocator {
		allocMu.Lock()
		p, err = owner.UintptrRealloc(ptr, int(size))
		allocMu.Unlock()
	} else {
		p, err = owner.UintptrRealloc(ptr, int(size))
	}
	if err != nil {
		if t != nil {
			t.setErrno(errno.ENOMEM)
		}
		return 0
	}
	registerPageOwner(p, owner)
	return p
}

// void free(void *ptr);
func Xfree(t *TLS, p uintptr) {
	if __ccgo_strace {
		trc("t=%v p=%v, (%v:)", t, p, origin(2))
	}
	if p == 0 {
		return
	}

	// Route free to whichever allocator owns the page. A pointer allocated
	// via nil-TLS (global) must free via the global allocator; a pointer
	// allocated via TLS-A must free via TLS-A's allocator — otherwise the
	// wrong allocator's page bookkeeping gets silently corrupted.
	owner := lookupPageOwner(p)
	if owner == nil {
		// Unknown pointer — fall back to the caller-preferred allocator.
		// This path keeps behavior backwards-compatible for anyone that
		// allocated before page-owner tracking was installed.
		if t != nil && t.allocator != nil {
			t.allocator.UintptrFree(p)
			return
		}
		allocMu.Lock()
		allocator.UintptrFree(p)
		allocMu.Unlock()
		return
	}
	if owner == &allocator {
		allocMu.Lock()
		owner.UintptrFree(p)
		allocMu.Unlock()
		return
	}
	// Per-TLS allocator — caller's TLS goroutine owns this allocator, so
	// no lock is needed (TLS.LockOSThread in the caller ensures exclusive
	// access per dedicated JS runtime goroutine).
	owner.UintptrFree(p)
}

func Xmalloc_usable_size(tls *TLS, p uintptr) (r types.Size_t) {
	if __ccgo_strace {
		trc("tls=%v p=%v, (%v:)", tls, p, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	if p == 0 {
		return 0
	}

	if tls != nil && tls.allocator != nil {
		// UintptrUsableSize reads the block's page header directly; no
		// allocator-global state is touched and the TLS owner has exclusive
		// access to its own pages, so the global lock is unnecessary.
		return types.Size_t(memory.UintptrUsableSize(p))
	}
	allocMu.Lock()
	defer allocMu.Unlock()
	return types.Size_t(memory.UintptrUsableSize(p))
}

func UsableSize(p uintptr) types.Size_t {
	allocMu.Lock()

	defer allocMu.Unlock()

	return types.Size_t(memory.UintptrUsableSize(p))
}

type MemAllocatorStat struct {
	Allocs int
	Bytes  int
	Mmaps  int
}

// MemStat returns the global memory allocator statistics.
// should be compiled with the memory.counters build tag for the data to be available.
func MemStat() MemAllocatorStat {
	allocMu.Lock()
	defer allocMu.Unlock()

	return MemAllocatorStat{
		Allocs: allocator.Allocs,
		Bytes:  allocator.Bytes,
		Mmaps:  allocator.Mmaps,
	}
}

// MemAuditStart locks the memory allocator, initializes and enables memory
// auditing. Finaly it unlocks the memory allocator.
//
// Some memory handling errors, like double free or freeing of unallocated
// memory, will panic when memory auditing is enabled.
//
// This memory auditing functionality has to be enabled using the libc.memgrind
// build tag.
//
// It is intended only for debug/test builds. It slows down memory allocation
// routines and it has additional memory costs.
func MemAuditStart() {}

// MemAuditReport locks the memory allocator, reports memory leaks, if any.
// Finally it disables memory auditing and unlocks the memory allocator.
//
// This memory auditing functionality has to be enabled using the libc.memgrind
// build tag.
//
// It is intended only for debug/test builds. It slows down memory allocation
// routines and it has additional memory costs.
func MemAuditReport() error { return nil }
