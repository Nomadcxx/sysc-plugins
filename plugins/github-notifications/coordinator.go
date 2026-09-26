package githubnotifications

import (
	"context"
	"sync"
)

type coordinatorJob struct {
	run     func(context.Context)
	done    func()
	refresh bool
}

// Coordinator serializes GitHub work off the plugin's input loop. Repeated
// refresh requests during one refresh collapse to one follow-up cycle.
type Coordinator struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu              sync.Mutex
	queue           []coordinatorJob
	refreshRunning  bool
	refreshPending  bool
	refreshRun      func(context.Context)
	refreshComplete func()

	wake      chan struct{}
	completed chan func()
	done      chan struct{}
}

func NewCoordinator(parent context.Context) *Coordinator {
	ctx, cancel := context.WithCancel(parent)
	c := &Coordinator{
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		completed: make(chan func(), 64), done: make(chan struct{}),
	}
	go c.loop()
	return c
}

func (c *Coordinator) Submit(run func(context.Context), complete func()) bool {
	if run == nil || c.ctx.Err() != nil {
		return false
	}
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return false
	}
	c.queue = append(c.queue, coordinatorJob{run: run, done: complete})
	c.mu.Unlock()
	c.signal()
	return true
}

func (c *Coordinator) RequestRefresh(run func(context.Context), complete func()) bool {
	if run == nil || c.ctx.Err() != nil {
		return false
	}
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return false
	}
	if c.refreshRunning {
		c.refreshPending = true
		c.mu.Unlock()
		return false
	}
	c.refreshRunning = true
	c.refreshRun = run
	c.refreshComplete = complete
	c.queue = append(c.queue, coordinatorJob{run: run, done: complete, refresh: true})
	c.mu.Unlock()
	c.signal()
	return true
}

func (c *Coordinator) Completed() <-chan func() { return c.completed }

func (c *Coordinator) Close() {
	c.cancel()
}

func (c *Coordinator) Done() <-chan struct{} { return c.done }

func (c *Coordinator) Refreshing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshRunning
}

func (c *Coordinator) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Coordinator) loop() {
	defer close(c.done)
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.wake:
		}
		for {
			job, ok := c.pop()
			if !ok {
				break
			}
			job.run(c.ctx)
			if job.refresh {
				c.finishRefresh()
			}
			if job.done != nil {
				select {
				case c.completed <- job.done:
				case <-c.ctx.Done():
					return
				}
			}
			if c.ctx.Err() != nil {
				return
			}
		}
	}
}

func (c *Coordinator) pop() (coordinatorJob, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queue) == 0 {
		return coordinatorJob{}, false
	}
	job := c.queue[0]
	c.queue[0] = coordinatorJob{}
	c.queue = c.queue[1:]
	return job, true
}

func (c *Coordinator) finishRefresh() {
	c.mu.Lock()
	if c.refreshPending && c.ctx.Err() == nil {
		c.refreshPending = false
		c.queue = append(c.queue, coordinatorJob{
			run: c.refreshRun, done: c.refreshComplete, refresh: true,
		})
		c.mu.Unlock()
		c.signal()
		return
	}
	c.refreshRunning = false
	c.refreshRun = nil
	c.refreshComplete = nil
	c.mu.Unlock()
}
