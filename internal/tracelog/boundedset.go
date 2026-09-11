package tracelog

// boundedSet is a membership set that evicts its oldest key once it holds
// capacity keys. Has never reports an unseen key, but can miss an evicted one.
type boundedSet[K comparable] struct {
	capacity int
	seen     map[K]struct{}
	order    []K
	head     int
}

func newBoundedSet[K comparable](capacity int) *boundedSet[K] {
	if capacity <= 0 {
		capacity = 1
	}
	return &boundedSet[K]{
		capacity: capacity,
		seen:     make(map[K]struct{}, capacity),
		order:    make([]K, capacity),
	}
}

func (b *boundedSet[K]) Has(k K) bool {
	_, ok := b.seen[k]
	return ok
}

func (b *boundedSet[K]) Add(k K) {
	if _, ok := b.seen[k]; ok {
		return
	}
	if len(b.seen) >= b.capacity {
		delete(b.seen, b.order[b.head])
	}
	b.seen[k] = struct{}{}
	b.order[b.head] = k
	b.head = (b.head + 1) % b.capacity
}

func (b *boundedSet[K]) Len() int { return len(b.seen) }
