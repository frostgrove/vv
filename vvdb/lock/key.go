package lock

import "hash/fnv"

type Key int64

func KeyOf(parts ...string) Key {
	digest := fnv.New64a()
	for index, part := range parts {
		if index > 0 {
			_, _ = digest.Write([]byte{0})
		}
		_, _ = digest.Write([]byte(part))
	}
	return Key(digest.Sum64())
}

func KeyFrom(raw int64) Key { return Key(raw) }

type Guard struct {
	Key    Key
	Shared bool
}

func Exclusively(key Key) Guard { return Guard{Key: key} }

func Sharing(key Key) Guard { return Guard{Key: key, Shared: true} }
