package cluster

// Gossip-based membership
//
// Protocol (SWIM-inspired, simplified):
//   - every node periodically sends its full member digest to ONE random
//     alive peer (epidemic dissemination)
//   - liveness = LastSeen timestamp bumped by the owner each tick
//   - merge rule: newer LastSeen wins; "dead" state is final and wins over
//     everything with a higher incarnation
//   - failure detection: no fresh LastSeen for suspectAfter -> suspect,
//     then deadAfter -> dead (excluded from fan-out)
//   - join: contact any configured seed, exchange digests, learn the rest
//     transitively
//   - leave: graceful POST /cluster/leave bumps incarnation + marks self dead

import (
	"bytes"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"time"
)

const (
	gossipInterval = 1 * time.Second
	suspectAfter   = 5 * time.Second
	deadAfter      = 15 * time.Second
)

// MemberState: alive -> suspect -> dead (dead is terminal).
type MemberState string

const (
	MemberAlive   MemberState = "alive"
	MemberSuspect MemberState = "suspect"
	MemberDead    MemberState = "dead"
)

// Member is one node's view of a cluster peer.
type Member struct {
	Name        string      `json:"name"`
	Addr        string      `json:"addr"` // host:port client endpoint
	Incarnation int64       `json:"inc"`  // bumped on refute/leave
	LastSeen    int64       `json:"seen"` // unix nano, set by the owner
	State       MemberState `json:"state"`
}

// StartMembership initialises gossip: self member, seed join, gossip loop.
// Seeds are remembered: if the initial join loses the startup race (peers
// not listening yet), the gossip loop keeps retrying (memberlist does the
// same with its join retry backoff).
func (n *Node) StartMembership(name, addr string, seeds []string) {
	n.self = Member{Name: name, Addr: addr, Incarnation: 1, State: MemberAlive}
	n.members = map[string]*Member{name: &n.self}
	n.seeds = seeds
	n.touchSelf()
	n.Join(seeds)
	go n.gossipLoop()
}

// touchSelf bumps our own heartbeat (called every tick).
func (n *Node) touchSelf() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.self.LastSeen = time.Now().UnixNano()
}

// digest returns a snapshot of the full member list.
func (n *Node) digest() []Member {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]Member, 0, len(n.members))
	for _, m := range n.members {
		out = append(out, *m)
	}
	return out
}

// merge folds an incoming digest into our member table.
func (n *Node) merge(in []Member) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, m := range in {
		if m.Name == n.self.Name {
			// Someone thinks we are suspect/dead -> refute by bumping incarnation.
			if m.State != MemberAlive && m.Incarnation >= n.self.Incarnation {
				n.self.Incarnation = m.Incarnation + 1
			}
			continue
		}
		cur, known := n.members[m.Name]
		if !known {
			cp := m
			n.members[m.Name] = &cp
			log.Printf("cluster: discovered member %s (%s)", m.Name, m.Addr)
			continue
		}
		// Dead with higher-or-equal incarnation is final.
		if m.State == MemberDead && m.Incarnation >= cur.Incarnation {
			if cur.State != MemberDead {
				log.Printf("cluster: member %s left or died", m.Name)
			}
			*cur = m
			continue
		}
		// Newer heartbeat refreshes liveness.
		if m.LastSeen > cur.LastSeen && m.Incarnation >= cur.Incarnation {
			*cur = m
		}
	}
}

// detectFailures transitions stale members alive->suspect->dead.
func (n *Node) detectFailures() {
	now := time.Now().UnixNano()
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, m := range n.members {
		if m.Name == n.self.Name || m.State == MemberDead {
			continue
		}
		silent := time.Duration(now - m.LastSeen)
		switch {
		case silent > deadAfter:
			if m.State != MemberDead {
				m.State = MemberDead
				log.Printf("cluster: member %s dead (silent %s)", m.Name, silent)
			}
		case silent > suspectAfter && m.State == MemberAlive:
			m.State = MemberSuspect
			log.Printf("cluster: member %s suspect (silent %s)", m.Name, silent)
		}
	}
}

// gossipLoop is the periodic heartbeat: update self, detect failures,
// exchange digests with one random alive peer (SWIM-style).
func (n *Node) gossipLoop() {
	tick := time.NewTicker(gossipInterval)
	defer tick.Stop()
	for range tick.C {
		n.touchSelf()
		n.detectFailures()
		peer := n.randomAlivePeer()
		if peer == "" {
			// No known alive peers: retry the seeds (join retry, as
			// memberlist does when a node boots before its peers).
			n.Join(n.seeds)
			continue
		}
		body, _ := json.Marshal(n.digest())
		resp, err := n.client.Post("http://"+peer+"/cluster/gossip", "application/json", bytes.NewReader(body))
		if err != nil {
			continue // failure detection handles it
		}
		var their []Member
		if json.NewDecoder(resp.Body).Decode(&their) == nil {
			n.merge(their)
		}
		resp.Body.Close()
	}
}

// randomAlivePeer picks a random alive non-self member address.
func (n *Node) randomAlivePeer() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var alive []string
	for _, m := range n.members {
		if m.Name != n.self.Name && m.State == MemberAlive {
			alive = append(alive, m.Addr)
		}
	}
	if len(alive) == 0 {
		return ""
	}
	return alive[rand.Intn(len(alive))]
}

// AliveAddrs returns addresses of members usable for fan-out (alive only).
func (n *Node) AliveAddrs() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var out []string
	for _, m := range n.members {
		if m.Name != n.self.Name && m.State == MemberAlive {
			out = append(out, m.Addr)
		}
	}
	return out
}

// Join contacts seed nodes until one answers; the rest of the cluster is
// learned transitively from its digest 
func (n *Node) Join(seeds []string) {
	body, _ := json.Marshal(n.digest())
	for _, seed := range seeds {
		if seed == n.self.Addr {
			continue
		}
		resp, err := n.client.Post("http://"+seed+"/cluster/gossip", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("cluster: seed %s unreachable: %v", seed, err)
			continue
		}
		var their []Member
		if json.NewDecoder(resp.Body).Decode(&their) == nil {
			n.merge(their)
		}
		resp.Body.Close()
		log.Printf("cluster: joined via seed %s, members=%d", seed, len(n.digest()))
		return
	}
}

// Leave announces graceful departure: incarnation bump + dead state,
// pushed to every known member 
func (n *Node) Leave() {
	n.mu.Lock()
	n.self.Incarnation++
	n.self.State = MemberDead
	self := n.self
	n.mu.Unlock()
	body, _ := json.Marshal([]Member{self})
	for _, addr := range n.AliveAddrs() {
		resp, err := n.client.Post("http://"+addr+"/cluster/gossip", "application/json", bytes.NewReader(body))
		if err == nil {
			resp.Body.Close()
		}
	}
}

// GossipHandler receives digests and replies with ours.
func (n *Node) GossipHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in []Member
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad digest", http.StatusBadRequest)
			return
		}
		n.merge(in)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(n.digest())
	}
}

// LeaveHandler lets an operator ask this node to leave gracefully.
func (n *Node) LeaveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n.Leave()
		w.WriteHeader(http.StatusNoContent)
	}
}

// MembersHandler is a debug endpoint: current member table.
func (n *Node) MembersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(n.digest())
	}
}
