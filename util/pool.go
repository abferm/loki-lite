package util

import (
	"errors"
	"sync"
)

var ErrPoolClosed = errors.New("pool is closed")

type Pool[T any] struct {
	constructor func() T
	destructor  func(T)
	max         int

	mu     sync.Mutex
	closed bool
	idle   []T
	total  int
	cond   *sync.Cond
}

func NewPool[T any](max int, constructor func() T, destructor func(T)) *Pool[T] {
	p := &Pool[T]{
		constructor: constructor,
		destructor:  destructor,
		max:         max,
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *Pool[T]) Acquire() (T, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		var zero T
		return zero, ErrPoolClosed
	}

	if len(p.idle) > 0 {
		v := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		return v, nil
	}

	if p.total < p.max {
		p.total++
		p.mu.Unlock()
		v := p.constructor()
		p.mu.Lock()
		if p.closed {
			p.destructor(v)
			p.total--
			var zero T
			return zero, ErrPoolClosed
		}
		return v, nil
	}

	for len(p.idle) == 0 && !p.closed {
		p.cond.Wait()
	}

	if len(p.idle) > 0 {
		v := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		return v, nil
	}

	var zero T
	return zero, ErrPoolClosed
}

func (p *Pool[T]) Release(v T) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		p.destructor(v)
		p.total--
		return
	}

	p.idle = append(p.idle, v)
	p.cond.Signal()
}

func (p *Pool[T]) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}
	p.closed = true

	for _, v := range p.idle {
		p.destructor(v)
		p.total--
	}
	p.idle = nil

	p.cond.Broadcast()
}
