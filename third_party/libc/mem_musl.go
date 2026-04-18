// Copyright 2023 The Libc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !libc.membrk && !libc.memgrind && linux && (amd64 || arm64 || loong64 || ppc64le || s390x || riscv64 || 386 || arm)

package libc // import "modernc.org/libc"

import (
	"math"
	mbits "math/bits"

	"modernc.org/memory"
)

const (
	isMemBrk = false
)

func Xmalloc(tls *TLS, n Tsize_t) (r uintptr) {
	if __ccgo_strace {
		trc("tls=%v n=%v, (%v:)", tls, n, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	if n > math.MaxInt {
		tls.setErrno(ENOMEM)
		return 0
	}

	if n == 0 {
		// malloc(0) should return unique pointers
		// (often expected and gnulib replaces malloc if malloc(0) returns 0)
		n = 1
	}

	var err error
	var owner *memory.Allocator
	if tls != nil && tls.allocator != nil {
		r, err = tls.allocator.UintptrMalloc(int(n))
		owner = tls.allocator
	} else {
		allocatorMu.Lock()
		r, err = allocator.UintptrMalloc(int(n))
		allocatorMu.Unlock()
		owner = &allocator
	}
	if err != nil {
		r = 0
		tls.setErrno(ENOMEM)
		return r
	}
	registerPageOwner(r, owner)
	return r
}

func Xcalloc(tls *TLS, m Tsize_t, n Tsize_t) (r uintptr) {
	if __ccgo_strace {
		trc("tls=%v m=%v n=%v, (%v:)", tls, m, n, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	hi, rq := mbits.Mul(uint(m), uint(n))
	if hi != 0 || rq > math.MaxInt {
		tls.setErrno(ENOMEM)
		return 0
	}

	if rq == 0 {
		rq = 1
	}

	var err error
	var owner *memory.Allocator
	if tls != nil && tls.allocator != nil {
		r, err = tls.allocator.UintptrCalloc(int(rq))
		owner = tls.allocator
	} else {
		allocatorMu.Lock()
		r, err = allocator.UintptrCalloc(int(rq))
		allocatorMu.Unlock()
		owner = &allocator
	}
	if err != nil {
		r = 0
		tls.setErrno(ENOMEM)
		return r
	}
	registerPageOwner(r, owner)
	return r
}

func Xrealloc(tls *TLS, p uintptr, n Tsize_t) (r uintptr) {
	if __ccgo_strace {
		trc("tls=%v p=%v n=%v, (%v:)", tls, p, n, origin(2))
		defer func() { trc("-> %v", r) }()
	}

	// Route realloc to whichever allocator currently owns p so bookkeeping
	// stays consistent. If p == 0 this acts like Xmalloc and we pick the
	// TLS allocator (or fall back to global).
	var owner *memory.Allocator
	if p != 0 {
		owner = lookupPageOwner(p)
	}
	if owner == nil {
		if tls != nil && tls.allocator != nil {
			owner = tls.allocator
		} else {
			owner = &allocator
		}
	}

	var err error
	if owner == &allocator {
		allocatorMu.Lock()
		r, err = owner.UintptrRealloc(p, int(n))
		allocatorMu.Unlock()
	} else {
		r, err = owner.UintptrRealloc(p, int(n))
	}
	if err != nil {
		r = 0
		tls.setErrno(ENOMEM)
		return r
	}
	registerPageOwner(r, owner)
	return r
}

func Xfree(tls *TLS, p uintptr) {
	if __ccgo_strace {
		trc("tls=%v p=%v, (%v:)", tls, p, origin(2))
	}
	if p == 0 {
		return
	}

	// Route free to whichever allocator owns the page. A pointer allocated
	// via nil-TLS (global) must free via the global allocator; a pointer
	// allocated via TLS-A must free via TLS-A's allocator - otherwise the
	// wrong allocator's page bookkeeping gets silently corrupted.
	owner := lookupPageOwner(p)
	if owner == nil {
		// Unknown pointer - fall back to the caller-preferred allocator.
		// Backwards-compatible path for pointers allocated before page-owner
		// tracking was installed.
		if tls != nil && tls.allocator != nil {
			tls.allocator.UintptrFree(p)
			return
		}
		allocatorMu.Lock()
		allocator.UintptrFree(p)
		allocatorMu.Unlock()
		return
	}
	if owner == &allocator {
		allocatorMu.Lock()
		owner.UintptrFree(p)
		allocatorMu.Unlock()
		return
	}
	// Per-TLS allocator - caller's TLS goroutine owns this allocator, so
	// no lock is needed (LockOSThread in the caller ensures exclusive
	// access per dedicated JS runtime goroutine).
	owner.UintptrFree(p)
}

func Xmalloc_usable_size(tls *TLS, p uintptr) (r Tsize_t) {
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
		return Tsize_t(memory.UintptrUsableSize(p))
	}

	allocatorMu.Lock()
	defer allocatorMu.Unlock()
	return Tsize_t(memory.UintptrUsableSize(p))
}

func MemAudit() (r []*MemAuditError) {
	return nil
}

func UsableSize(p uintptr) Tsize_t {
	allocatorMu.Lock()

	defer allocatorMu.Unlock()

	return Tsize_t(memory.UintptrUsableSize(p))
}

type MemAllocatorStat struct {
	Allocs int
	Bytes  int
	Mmaps  int
}

// MemStat returns the global memory allocator statistics.
// should be compiled with the memory.counters build tag for the data to be available.
func MemStat() MemAllocatorStat {
	allocatorMu.Lock()
	defer allocatorMu.Unlock()

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
