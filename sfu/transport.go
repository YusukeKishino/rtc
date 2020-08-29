package sfu

import "github.com/pion/webrtc/v2"

type Transport interface {
	ID() string
	GetRouter(uint32) *Router
	Routers() map[uint32]*Router
	NewSender(track *webrtc.Track) (Sender, error)
	stats() string
}
