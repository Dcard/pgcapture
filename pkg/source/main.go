package source

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/rueian/pgcapture/pkg/pb"
)

type Checkpoint struct {
	LSN  uint64
	Time time.Time
}

type Change struct {
	Checkpoint Checkpoint
	Message    *pb.Message
}

type Source interface {
	Capture(cp Checkpoint) (changes chan Change, err error)
	Commit(cp Checkpoint)
	Error() error
	Stop() error
}

type RequeueSource interface {
	Source
	Requeue(cp Checkpoint)
}

type BaseSource struct {
	ReadTimeout time.Duration

	state   int64
	stopped chan struct{}

	err atomic.Value
}

func (b *BaseSource) Capture(cp Checkpoint) (changes chan Change, err error) {
	panic("implement me")
}

func (b *BaseSource) Commit(cp Checkpoint) {
	panic("implement me")
}

func (b *BaseSource) Stop() error {
	switch atomic.LoadInt64(&b.state) {
	case 1, 2:
		for atomic.LoadInt64(&b.state) != 2 {
		}
		atomic.CompareAndSwapInt64(&b.state, 2, 3)
		fallthrough
	case 3:
		<-b.stopped
	}
	return b.Error()
}

func (b *BaseSource) Error() error {
	if err, ok := b.err.Load().(error); ok {
		return err
	}
	return nil
}

func (b *BaseSource) capture(readFn ReadFn, flushFn FlushFn) (chan Change, error) {
	if !atomic.CompareAndSwapInt64(&b.state, 0, 1) {
		return nil, nil
	}

	b.stopped = make(chan struct{})
	changes := make(chan Change, 100)

	atomic.StoreInt64(&b.state, 2)

	timeout := b.ReadTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	go func() {
		defer close(b.stopped)
		defer close(changes)
		defer flushFn()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			change, err := readFn(ctx)
			cancel()
			if atomic.LoadInt64(&b.state) != 2 {
				return
			}
			if isTimeout(err) {
				continue
			}
			if err != nil {
				b.err.Store(err)
				return
			}
			if change.Message != nil {
				changes <- change
			}
		}
	}()
	return changes, nil
}

type CaptureFn func(changes chan Change) error
type FlushFn func()
type ReadFn func(ctx context.Context) (Change, error)

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
