// Package lru retains the shared bounded cache API.
package lru

import kit "github.com/braedonsaunders/ether/internal/frontend/kit/lru"

type Options[K comparable, V any] = kit.Options[K, V]
type Cache[K comparable, V any] = kit.Cache[K, V]

func New[K comparable, V any](opts Options[K, V]) *Cache[K, V] { return kit.New(opts) }
