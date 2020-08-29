package sfu

import (
	"sync"
	"time"

	"github.com/pion/webrtc/v2"
	"github.com/sirupsen/logrus"
)

type SFU struct {
	webrtc   WebRTCTransportConfig
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSFU(c Config) *SFU {
	w := WebRTCTransportConfig{
		configuration: webrtc.Configuration{
			SDPSemantics: webrtc.SDPSemanticsUnifiedPlan,
		},
		setting: webrtc.SettingEngine{},
	}
	s := &SFU{
		webrtc:   w,
		sessions: make(map[string]*Session),
	}

	config = c

	var icePortStart, icePortEnd uint16

	if len(c.WebRTC.ICEPortRange) == 2 {
		icePortStart = c.WebRTC.ICEPortRange[0]
		icePortEnd = c.WebRTC.ICEPortRange[1]
	}

	if icePortStart != 0 || icePortEnd != 0 {
		if err := s.webrtc.setting.SetEphemeralUDPPortRange(icePortStart, icePortEnd); err != nil {
			panic(err)
		}
	}

	s.webrtc.configuration.ICEServers = []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	}

	go s.stats()

	return s
}

// NewSession creates a new session instance
func (s *SFU) newSession(id string) *Session {
	session := NewSession(id)
	session.OnClose(func() {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	})

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()
	return session
}

// GetSession by id
func (s *SFU) getSession(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// NewWebRTCTransport creates a new WebRTCTransport that is a member of a session
func (s *SFU) NewWebRTCTransport(sid string, offer webrtc.SessionDescription) (*WebRTCTransport, error) {
	session := s.getSession(sid)

	if session == nil {
		session = s.newSession(sid)
	}

	t, err := NewWebRTCTransport(session, offer, s.webrtc)
	if err != nil {
		return nil, err
	}

	return t, nil
}

func (s *SFU) stats() {
	t := time.NewTicker(statCycle)
	for range t.C {
		info := "\n----------------stats-----------------\n"

		s.mu.RLock()
		if len(s.sessions) == 0 {
			s.mu.RUnlock()
			continue
		}

		for _, session := range s.sessions {
			info += session.stats()
		}
		s.mu.RUnlock()
		logrus.Infof(info)
	}
}
