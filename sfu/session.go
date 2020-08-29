package sfu

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

type Session struct {
	id             string
	transports     map[string]Transport
	mu             sync.RWMutex
	onCloseHandler func()
}

func NewSession(id string) *Session {
	return &Session{
		id:         id,
		transports: make(map[string]Transport),
	}
}

func (r *Session) AddTransport(transport Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.transports[transport.ID()] = transport
}

func (r *Session) RemoveTransport(tid string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.transports, tid)

	for _, t := range r.transports {
		for _, router := range t.Routers() {
			router.DelSub(tid)
		}
	}

	if len(r.transports) == 0 && r.onCloseHandler != nil {
		r.onCloseHandler()
	}
}

func (r *Session) AddRouter(router *Router) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for tid, t := range r.transports {
		if router.tid == tid {
			continue
		}

		logrus.Infof("AddRouter ssrc %d to %s", router.Track().SSRC(), tid)

		sender, err := t.NewSender(router.Track())

		if err != nil {
			logrus.Errorf("Error subscribing transport to router: %s", err)
			continue
		}

		// Attach sender to source
		router.AddSender(tid, sender)

		if t.(*WebRTCTransport).onNegotiationNeededHandler != nil {
			t.(*WebRTCTransport).onNegotiationNeededHandler()
		}
	}
}

// Transports returns transports in this session
func (r *Session) Transports() map[string]Transport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transports
}

// OnClose called when session is closed
func (r *Session) OnClose(f func()) {
	r.onCloseHandler = f
}

func (r *Session) stats() string {
	info := fmt.Sprintf("\nsession: %s\n", r.id)

	r.mu.RLock()
	for _, transport := range r.transports {
		info += transport.stats()
	}
	r.mu.RUnlock()

	return info
}
