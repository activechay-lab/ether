// Package paging applies bounded offset/limit windows without integer overflow.
package paging

import (
	"errors"
	"strconv"
)

// Bounds returns a safe half-open slice interval. A negative offset is treated
// as zero; a negative limit means no limit, while zero requests an empty page.
func Bounds(total, offset, limit int) (start, end int) {
	if total < 0 {
		total = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	start = offset
	if limit < 0 || limit > total-start {
		return start, total
	}
	return start, start + limit
}

// Slice returns the requested page, preserving the source order.
func Slice[T any](items []T, offset, limit int) []T {
	start, end := Bounds(len(items), offset, limit)
	return items[start:end]
}

// ParseLimit accepts an empty query value as the default and rejects invalid bounds.
func ParseLimit(raw string, fallback, maximum int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maximum {
		return 0, errors.New("invalid limit")
	}
	return n, nil
}

// ParseOffset rejects offsets whose next page could overflow an int.
func ParseOffset(raw string, limit int) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > int(^uint(0)>>1)-max(0, limit) {
		return 0, errors.New("invalid offset")
	}
	return n, nil
}

// PageOffset saturates a one-based page multiplication rather than overflowing.
func PageOffset(page, size int) int {
	if page <= 1 || size <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	if page-1 > (maxInt-size)/size {
		return maxInt - size
	}
	return (page - 1) * size
}
