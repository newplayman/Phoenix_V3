package strategy

import (
	"container/heap"
	"sync"
)

// IntentQueue connects Strategy -> Gateway
// It's a thread-safe priority queue.
type IntentQueue struct {
	pq   PriorityQueue
	mu   sync.Mutex
	cond *sync.Cond
}

func NewIntentQueue() *IntentQueue {
	iq := &IntentQueue{
		pq: make(PriorityQueue, 0),
	}
	iq.cond = sync.NewCond(&iq.mu)
	return iq
}

func (iq *IntentQueue) Push(intent Intent) {
	iq.mu.Lock()
	defer iq.mu.Unlock()

	heap.Push(&iq.pq, &intent)
	iq.cond.Signal()
}

func (iq *IntentQueue) Pop() Intent {
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

// PriorityQueue implementation
type PriorityQueue []*Intent

func (pq PriorityQueue) Len() int { return len(pq) }

// Less: Higher Urgency comes first
func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].Urgency > pq[j].Urgency
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *PriorityQueue) Push(x interface{}) {
	item := x.(*Intent)
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}
