package strategy

import (
	"container/heap"
	"sort"
	"sync"
)

// IntentQueue connects Strategy -> Gateway
// It's a thread-safe priority queue.
type IntentQueue struct {
	pq   priorityQueue
	mu   sync.Mutex
	cond *sync.Cond
}

func NewIntentQueue() *IntentQueue {
	iq := &IntentQueue{
		pq: make(priorityQueue, 0),
	}
	iq.cond = sync.NewCond(&iq.mu)
	return iq
}

func (iq *IntentQueue) Enqueue(intent Intent) {
	iq.mu.Lock()
	defer iq.mu.Unlock()

	heap.Push(&iq.pq, &intent)
	iq.cond.Signal()
}

func (iq *IntentQueue) Dequeue() Intent {
	iq.mu.Lock()
	defer iq.mu.Unlock()

	for iq.pq.Len() == 0 {
		iq.cond.Wait()
	}

	item := heap.Pop(&iq.pq).(*Intent)
	return *item
}

func (iq *IntentQueue) Len() int {
	iq.mu.Lock()
	defer iq.mu.Unlock()
	return iq.pq.Len()
}

// Snapshot returns a copy of up to limit pending intents, ordered by priority.
// limit <= 0 returns all.
func (iq *IntentQueue) Snapshot(limit int) []Intent {
	iq.mu.Lock()
	defer iq.mu.Unlock()
	if limit <= 0 || limit > iq.pq.Len() {
		limit = iq.pq.Len()
	}
	// Copy pointers and sort by priority without mutating heap.
	items := make([]*Intent, 0, iq.pq.Len())
	items = append(items, iq.pq...)
	sorted := make(priorityQueue, len(items))
	copy(sorted, items)
	// Use heap ordering rules via sort.
	// Since priorityQueue implements Less, sort.Slice can be used.
	sort.Slice(sorted, func(i, j int) bool { return sorted.Less(i, j) })
	out := make([]Intent, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, *sorted[i])
	}
	return out
}

// priorityQueue implements heap.Interface
type priorityQueue []*Intent

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].Urgency == pq[j].Urgency {
		return pq[i].Deadline.Before(pq[j].Deadline)
	}
	return pq[i].Urgency > pq[j].Urgency
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *priorityQueue) Push(x interface{}) {
	item := x.(*Intent)
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}
