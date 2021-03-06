package batcher

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Batcher interface {
	Push(ctx context.Context, v interface{}) error
	Run(ctx context.Context) error
	Flush(ctx context.Context) error
	Counters() (items int64, batches int64)
}

type CallbackFunc func(context.Context, []interface{}) error

func New(n int, d time.Duration, f CallbackFunc) Batcher {
	return newBatcher(n, 0, d, f)
}

func NewBuffer(n, l int, d time.Duration, f CallbackFunc) Batcher {
	return newBatcher(n, l, d, f)
}

func newBatcher(n, l int, d time.Duration, f CallbackFunc) *batcher {
	return &batcher{
		n:   n,
		d:   d,
		f:   f,
		ch:  make(chan interface{}, l),
		buf: make([]interface{}, 0, n),
	}
}

type batcher struct {
	n int           // batch size limit
	d time.Duration // batch time limit
	f CallbackFunc

	t   *time.Timer
	ch  chan interface{}
	buf []interface{}
	mux sync.Mutex

	items   int64
	batches int64
}

func (b *batcher) Push(ctx context.Context, i interface{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.ch <- i:
		atomic.AddInt64(&b.items, 1)
		return nil
	}
}

func (b *batcher) Run(ctx context.Context) error {
	return b.loop(ctx)
}

func (b *batcher) Flush(ctx context.Context) error {
	if len(b.buf) == 0 {
		return nil
	}

	b.mux.Lock()
	defer b.mux.Unlock()

	if err := b.f(ctx, b.buf); err != nil {
		return err
	}
	b.buf = b.buf[0:0]
	return nil
}

func (b *batcher) Counters() (int64, int64) {
	return atomic.LoadInt64(&b.items), atomic.LoadInt64(&b.batches)
}

func (b *batcher) loop(ctx context.Context) error {
	for {
		if err := b.load(ctx); err != nil {
			return err
		}
		if len(b.buf) == 0 {
			continue
		}
		if err := b.Flush(ctx); err != nil {
			return err
		}
		atomic.AddInt64(&b.batches, 1)
	}
}

func (b *batcher) load(ctx context.Context) (err error) {
	if b.t == nil {
		b.t = time.NewTimer(b.d)
	} else {
		b.t.Reset(b.d)
	}
	for {
		select {
		case <-b.t.C:
			return nil
		case <-ctx.Done():
			if !b.t.Stop() {
				<-b.t.C
			}
			return ctx.Err()
		case i := <-b.ch:
			b.buf = append(b.buf, i)
			if len(b.buf) == b.n {
				if !b.t.Stop() {
					<-b.t.C
				}
				return nil
			}
		}
	}
}
