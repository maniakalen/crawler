package queue

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Queue struct {
	q      []interface{}
	In     chan interface{}
	Out    chan interface{}
	mux    sync.Mutex
	ctx    *context.Context
	cancel *context.CancelFunc
}

func (t *Queue) Add(i interface{}) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.q = append(t.q, i)
}

func (t *Queue) Pop() (interface{}, error) {
	t.mux.Lock()
	defer t.mux.Unlock()
	if len(t.q) > 0 {
		out := t.q[0]
		t.q = t.q[1:]
		return out, nil
	}
	return nil, fmt.Errorf("queue is empty")
}

func (t *Queue) Close() {
	if (*t.ctx).Err() != nil {
		return
	}
	t.q = []interface{}{}
	(*t.cancel)()
	close(t.In)
	close(t.Out)
}

func (t *Queue) Size() int {
	t.mux.Lock()
	defer t.mux.Unlock()
	return len(t.q)
}

func New(parentCtx context.Context) *Queue {
	ctx, cancel := context.WithCancel(parentCtx)
	in := make(chan interface{}, 20)
	out := make(chan interface{})
	queue := Queue{q: []interface{}{}, In: in, Out: out, ctx: &ctx, cancel: &cancel}
	go func() {
		for {
			select {
			case i := <-in:
				queue.Add(i)
			case <-(*queue.ctx).Done():
				return
			}
		}
	}()
	go func() {
		defer cancel()
		for {
			select {
			case <-(*queue.ctx).Done():
				return
			case <-time.After(5 * time.Second):
				return
			default:
				o, err := queue.Pop()
				if err == nil {
					out <- o
				}
			}
		}
	}()
	return &queue
}
