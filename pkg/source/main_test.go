package source

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rueian/pgcapture/pkg/pb"
)

type source struct {
	BaseSource
	ReadFn  ReadFn
	Flushed chan struct{}
}

func (s *source) Capture(cp Checkpoint) (changes chan Change, err error) {
	s.Flushed = make(chan struct{})
	return s.BaseSource.capture(s.ReadFn, func() {
		close(s.Flushed)
	})
}

func (s *source) Commit(cp Checkpoint) {

}

var ErrAny = errors.New("error")

func TestBaseSource_Stop(t *testing.T) {
	source := source{
		BaseSource: BaseSource{ReadTimeout: time.Second},
		ReadFn: func(ctx context.Context) (Change, error) {
			return Change{Message: &pb.Message{}}, ctx.Err()
		},
	}
	changes, _ := source.Capture(Checkpoint{})

	go func() {
		time.Sleep(time.Second / 2)
		source.Stop()
	}()

	for range changes {
	}

	if _, more := <-changes; more {
		t.Fatal("committed channel should be closed after stop")
	}

	if _, more := <-source.Flushed; more {
		t.Fatal("clean func should be called once")
	}

	if source.Error() != nil {
		t.Fatalf("unexpected %v", source.Error())
	}
}

func TestBaseSource_SecondCapture(t *testing.T) {
	source := source{
		BaseSource: BaseSource{ReadTimeout: time.Second},
		ReadFn: func(ctx context.Context) (Change, error) {
			return Change{Message: &pb.Message{}}, ctx.Err()
		},
	}
	changes, _ := source.Capture(Checkpoint{})

	if second, err := source.Capture(Checkpoint{}); second != nil || err != nil {
		t.Fatal("second capture should be ignore")
	}

	source.Stop()

	for range changes {
	}

	if _, more := <-changes; more {
		t.Fatal("committed channel should be closed after stop")
	}

	if _, more := <-source.Flushed; more {
		t.Fatal("clean func should be called once")
	}

	if source.Error() != nil {
		t.Fatalf("unexpected %v", source.Error())
	}
}

func TestBaseSource_Timeout(t *testing.T) {
	count := 0
	source := source{
		BaseSource: BaseSource{ReadTimeout: time.Second / 5},
		ReadFn: func(ctx context.Context) (Change, error) {
			if count == 0 {
				time.Sleep(time.Second / 3)
			}
			count++
			return Change{Message: &pb.Message{}}, ctx.Err()
		},
	}
	changes, _ := source.Capture(Checkpoint{})

	go func() {
		time.Sleep(time.Second / 2)
		source.Stop()
	}()

	for range changes {
	}

	if _, more := <-changes; more {
		t.Fatal("committed channel should be closed")
	}
	if _, more := <-source.Flushed; more {
		t.Fatal("clean func should be called once")
	}

	if source.Error() != nil {
		t.Fatalf("unexpected %v", source.Error())
	}
}

func TestBaseSource_Error(t *testing.T) {
	source := source{
		BaseSource: BaseSource{ReadTimeout: time.Second},
		ReadFn: func(ctx context.Context) (Change, error) {
			return Change{}, ErrAny
		},
	}
	changes, _ := source.Capture(Checkpoint{})

	if _, more := <-changes; more {
		t.Fatal("committed channel should be closed")
	}
	if _, more := <-source.Flushed; more {
		t.Fatal("clean func should be called once")
	}

	if !errors.Is(source.Error(), ErrAny) {
		t.Fatalf("unexpected %v", source.Error())
	}
}

func TestBaseSink_CapturePanic(t *testing.T) {
	defer func() { recover() }()
	s := BaseSource{}
	s.Capture(Checkpoint{})
	t.Fatal("should panic")
}

func TestBaseSink_CommitPanic(t *testing.T) {
	defer func() { recover() }()
	s := BaseSource{}
	s.Commit(Checkpoint{})
	t.Fatal("should panic")
}
