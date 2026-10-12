// Package cluster is a minimal multi-node fan-out layer.
// gRPC (message_router.go). Helix simplifies: a static peer list from the
// config, and every node forwards stream messages to all peers over plain
// HTTP JSON. Each node only delivers to its LOCAL sessions, so a message
// is delivered exactly once per session.
package cluster

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"helix/internal/tracker"
)

// Node is one helix instance in a cluster.
type Node struct {
	peers  []string // host:port client endpoints of OTHER nodes
	client *http.Client
	tr     *tracker.Tracker
}

func New(peers []string, tr *tracker.Tracker) *Node {
	return &Node{
		peers:  peers,
		tr:     tr,
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

// StreamMessage is the wire format exchanged between nodes.
type StreamMessage struct {
	Stream string          `json:"stream"`
	Op     string          `json:"op"`
	Data   json.RawMessage `json:"data"`
}

// Fanout sends a stream message to every peer node. Delivery on the local
// node is done by the caller; errors are logged but never fatal — a dead
// peer must not break local gameplay 
func (n *Node) Fanout(stream, op string, data any) {
	if len(n.peers) == 0 {
		return
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	body, err := json.Marshal(StreamMessage{Stream: stream, Op: op, Data: payload})
	if err != nil {
		return
	}
	for _, peer := range n.peers {
		go func(addr string) {
			resp, err := n.client.Post("http://"+addr+"/cluster/send", "application/json", bytes.NewReader(body))
			if err != nil {
				log.Printf("cluster: fan-out to %s failed: %v", addr, err)
				return
			}
			resp.Body.Close()
		}(peer)
	}
}

// ReceiveHandler accepts fan-out messages from peers and delivers them to
// LOCAL sessions of the target stream
func (n *Node) ReceiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var msg StreamMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "bad message", http.StatusBadRequest)
			return
		}
		payload, err := json.Marshal(struct {
			Op   string          `json:"op"`
			Data json.RawMessage `json:"data"`
		}{msg.Op, msg.Data})
		if err != nil {
			http.Error(w, "bad message", http.StatusBadRequest)
			return
		}
		for _, s := range n.tr.List(msg.Stream) {
			s.Send(payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Peers returns the configured peer list (for the /health endpoint).
func (n *Node) Peers() []string { return n.peers }
