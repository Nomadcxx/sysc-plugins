package hostcall

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// StateSaver sends state.set calls on one worker. Pending values coalesce by
// key, so an input burst cannot block the event loop or leave an older value
// queued behind the call already in flight.
type StateSaver struct {
	c        *v1.Client
	ctx      context.Context
	timeout  time.Duration
	mu       sync.Mutex
	pending  map[string]stateValue
	revision map[string]uint64
	signal   chan struct{}
	write    sync.Mutex
}

type stateValue struct {
	value    json.RawMessage
	revision uint64
	results  chan StateWriteResult
}

// StateWriteResult reports the reply for a tracked state.set call.
type StateWriteResult struct {
	Key      string
	Revision uint64
	Reply    v1.HostReply
	Err      error
}

func NewStateSaver(ctx context.Context, c *v1.Client) *StateSaver {
	return NewStateSaverWithTimeout(ctx, c, 0)
}

// NewStateSaverWithTimeout bounds state.set calls to timeout when it is
// positive; zero keeps the package default.
func NewStateSaverWithTimeout(ctx context.Context, c *v1.Client, timeout time.Duration) *StateSaver {
	s := &StateSaver{
		c: c, ctx: ctx, timeout: timeout, pending: make(map[string]stateValue),
		revision: make(map[string]uint64), signal: make(chan struct{}, 1),
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.signal:
				s.drain(ctx, 0)
			}
		}
	}()
	return s
}

// Save replaces any unsent value for key and returns without waiting for host.
func (s *StateSaver) Save(key string, value json.RawMessage) {
	s.enqueue(key, value, nil)
}

// SaveTracked coalesces like Save and reports the latest call's host reply on
// results. The channel should be buffered; reporting never blocks the writer.
func (s *StateSaver) SaveTracked(key string, value json.RawMessage, results chan StateWriteResult) uint64 {
	return s.enqueue(key, value, results)
}

func (s *StateSaver) enqueue(key string, value json.RawMessage, results chan StateWriteResult) uint64 {
	s.mu.Lock()
	s.revision[key]++
	revision := s.revision[key]
	s.pending[key] = stateValue{value: append(json.RawMessage(nil), value...), revision: revision, results: results}
	s.mu.Unlock()
	select {
	case s.signal <- struct{}{}:
	default:
	}
	return revision
}

// Flush writes pending values without inheriting cancellation from the plugin
// context. It is intended for shutdown, and bounds each host call by timeout.
func (s *StateSaver) Flush(timeout time.Duration) {
	s.drain(context.WithoutCancel(s.ctx), timeout)
}

func (s *StateSaver) drain(ctx context.Context, timeout time.Duration) {
	s.write.Lock()
	defer s.write.Unlock()
	s.mu.Lock()
	pending := s.pending
	s.pending = make(map[string]stateValue)
	s.mu.Unlock()
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		callCtx := ctx
		cancel := func() {}
		limit := s.timeout
		if timeout > 0 && (limit == 0 || timeout < limit) {
			limit = timeout
		}
		if limit > 0 {
			callCtx, cancel = context.WithTimeout(callCtx, limit)
		}
		reply, err := Call(callCtx, s.c, v1.CallStateSet, v1.StateSetParams{Key: key, Value: pending[key].value})
		cancel()
		value := pending[key]
		s.mu.Lock()
		latest := s.revision[key]
		if latest == value.revision && (err != nil || !reply.OK) {
			// Keep failed writes available for shutdown Flush. A newer Save has
			// its own pending value and must not be replaced by this one.
			s.pending[key] = value
		}
		s.mu.Unlock()
		if value.results != nil {
			if latest == value.revision {
				result := StateWriteResult{Key: key, Revision: value.revision, Reply: reply, Err: err}
				select {
				case value.results <- result:
				default:
					select {
					case <-value.results:
					default:
					}
					select {
					case value.results <- result:
					default:
					}
				}
			}
		}
	}
}
