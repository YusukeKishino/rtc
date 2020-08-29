package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pion/webrtc/v2"
	"github.com/sirupsen/logrus"
	"github.com/sourcegraph/jsonrpc2"

	"github.com/YusukeKishino/rtc/sfu"
)

type Handler struct {
	sfu *sfu.SFU
}

func NewHandler() *Handler {
	return &Handler{
		sfu: sfu.NewSFU(sfu.Config{
			WebRTC: sfu.WebRTCConfig{
				ICEPortRange: []uint16{50000, 60000},
			},
			Receiver: sfu.ReceiverConfig{
				Video: sfu.WebRTCVideoReceiverConfig{
					REMBCycle:     2,
					PLICycle:      1,
					TCCCycle:      1,
					MaxBandwidth:  1000,
					MaxBufferTime: 1000,
				},
			},
		}),
	}
}

type peerContext struct {
	peer *sfu.WebRTCTransport
}

type contextKey struct {
	name string
}

var peerCtxKey = &contextKey{"peer"}

func forContext(ctx context.Context) *peerContext {
	raw, _ := ctx.Value(peerCtxKey).(*peerContext)
	return raw
}

// Join message sent when initializing a peer connection
type Join struct {
	Sid   string                    `json:"sid"`
	Offer webrtc.SessionDescription `json:"offer"`
}

// Negotiation message sent when renegotiating
type Negotiation struct {
	Desc webrtc.SessionDescription `json:"desc"`
}

// Trickle message sent when renegotiating
type Trickle struct {
	Candidate webrtc.ICECandidateInit `json:"candidate"`
}

func (h *Handler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	p := forContext(ctx)

	switch req.Method {
	case "join":
		if p.peer != nil {
			logrus.Errorf("connect: peer already exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("peer already exists")),
			})
			break
		}

		var join Join
		err := json.Unmarshal(*req.Params, &join)
		if err != nil {
			logrus.Errorf("connect: error parsing offer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}
		peer, err := h.sfu.NewWebRTCTransport(join.Sid, join.Offer)

		if err != nil {
			logrus.Errorf("connect: error creating peer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		logrus.Infof("peer %s join session %s", peer.ID(), join.Sid)

		err = peer.SetRemoteDescription(join.Offer)
		if err != nil {
			logrus.Errorf("Offer error: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}
		answer, err := peer.CreateAnswer()
		if err != nil {
			logrus.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		err = peer.SetLocalDescription(answer)
		if err != nil {
			logrus.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		// Notify user of trickle candidates
		peer.OnICECandidate(func(c *webrtc.ICECandidate) {
			logrus.Debugf("Sending ICE candidate")
			if c == nil {
				// Gathering done
				return
			}

			if err := conn.Notify(ctx, "trickle", c.ToJSON()); err != nil {
				logrus.Errorf("error sending trickle %s", err)
			}
		})

		peer.OnNegotiationNeeded(func() {
			logrus.Debugf("on negotiation needed called")
			offer, err := p.peer.CreateOffer()
			if err != nil {
				logrus.Errorf("CreateOffer error: %v", err)
				return
			}

			err = p.peer.SetLocalDescription(offer)
			if err != nil {
				logrus.Errorf("SetLocalDescription error: %v", err)
				return
			}

			if err := conn.Notify(ctx, "offer", offer); err != nil {
				logrus.Errorf("error sending offer %s", err)
			}
		})

		p.peer = peer

		_ = conn.Reply(ctx, req.ID, answer)

		// Hack until renegotation is supported in pion. Force renegotation incase there are unmatched
		// receviers (i.e. sfu has more than one sender). We just naively create server offer. It is
		// noop if things are already matched. We can remove once https://github.com/pion/webrtc/pull/1322
		// is merged
		time.Sleep(1000 * time.Millisecond)

		logrus.Debugf("on negotiation needed called")
		offer, err := p.peer.CreateOffer()
		if err != nil {
			logrus.Errorf("CreateOffer error: %v", err)
			return
		}

		err = p.peer.SetLocalDescription(offer)
		if err != nil {
			logrus.Errorf("SetLocalDescription error: %v", err)
			return
		}

		if err := conn.Notify(ctx, "offer", offer); err != nil {
			logrus.Errorf("error sending offer %s", err)
		}

	case "offer":
		if p.peer == nil {
			logrus.Errorf("connect: no peer exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		logrus.Infof("peer %s offer", p.peer.ID())

		var negotiation Negotiation
		err := json.Unmarshal(*req.Params, &negotiation)
		if err != nil {
			logrus.Errorf("connect: error parsing offer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		// Peer exists, renegotiating existing peer
		err = p.peer.SetRemoteDescription(negotiation.Desc)
		if err != nil {
			logrus.Errorf("Offer error: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		answer, err := p.peer.CreateAnswer()
		if err != nil {
			logrus.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		err = p.peer.SetLocalDescription(answer)
		if err != nil {
			logrus.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		_ = conn.Reply(ctx, req.ID, answer)

	case "answer":
		if p.peer == nil {
			logrus.Errorf("connect: no peer exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		logrus.Infof("peer %s answer", p.peer.ID())

		var negotiation Negotiation
		err := json.Unmarshal(*req.Params, &negotiation)
		if err != nil {
			logrus.Errorf("connect: error parsing answer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		err = p.peer.SetRemoteDescription(negotiation.Desc)
		if err != nil {
			logrus.Errorf("error setting remote description %s", err)
		}

	case "trickle":
		logrus.Debugf("trickle")
		if p.peer == nil {
			logrus.Errorf("connect: no peer exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		logrus.Infof("peer %s trickle", p.peer.ID())

		var trickle Trickle
		err := json.Unmarshal(*req.Params, &trickle)
		if err != nil {
			logrus.Errorf("connect: error parsing candidate: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		err = p.peer.AddICECandidate(trickle.Candidate)
		if err != nil {
			logrus.Errorf("error setting ice candidate %s", err)
		}
	}
}
