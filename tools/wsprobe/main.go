// wsprobe is a scripted WebSocket test client for helix.
// Usage: go run ./tools/wsprobe <scenario>
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type client struct {
	name string
	conn *websocket.Conn
	in   chan map[string]any
}

func auth(deviceID, username string) string {
	body := fmt.Sprintf(`{"device_id":%q,"username":%q}`, deviceID, username)
	resp, err := http.Post("http://localhost:7350/auth/device", "application/json", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		panic("auth failed: " + string(raw))
	}
	return out.Token
}

func userID(deviceID string) string {
	body := fmt.Sprintf(`{"device_id":%q}`, deviceID)
	resp, err := http.Post("http://localhost:7350/auth/device", "application/json", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.UserID
}

func dial(name, token string) *client {
	c, _, err := websocket.DefaultDialer.Dial("ws://localhost:7350/ws?token="+token, nil)
	if err != nil {
		panic(err)
	}
	cl := &client{name: name, conn: c, in: make(chan map[string]any, 64)}
	go func() {
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				close(cl.in)
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) == nil {
				cl.in <- msg
			}
		}
	}()
	return cl
}

func (c *client) send(cid, op string, data any) {
	raw, _ := json.Marshal(data)
	payload, _ := json.Marshal(map[string]any{"cid": cid, "op": op, "data": json.RawMessage(raw)})
	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		panic(err)
	}
}

// waitFor returns the first message whose op matches, within the timeout.
func (c *client) waitFor(op string, timeout time.Duration) (map[string]any, bool) {
	return c.waitForMatch(op, timeout, "")
}

// waitForMatch additionally requires the payload to contain a substring.
func (c *client) waitForMatch(op string, timeout time.Duration, contains string) (map[string]any, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-c.in:
			if !ok {
				return nil, false
			}
			if msg["op"] == op && (contains == "" || strings.Contains(fmt.Sprint(msg["data"]), contains)) {
				return msg, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

func expect(label string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("[%s] %s %s\n", status, label, detail)
}

func ok2(m map[string]any) bool { return m != nil && !strings.Contains(fmt.Sprint(m["data"]), "error") }

func main() {
	scenario := "match"
	if len(os.Args) > 1 {
		scenario = os.Args[1]
	}

	switch scenario {
	case "parties":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		bob := dial("Bob", auth("dev-bob", "Bob"))
		carol := dial("Carol", auth("dev-carol", "Carol"))

		// 1. Alice creates a party, Bob joins, both notified.
		alice.send("1", "party.create", map[string]any{"open": true, "max_size": 2})
		c, ok := alice.waitFor("party.create", 2*time.Second)
		expect("party created", ok, fmt.Sprint(c["data"]))
		pid, _ := c["data"].(map[string]any)["party_id"].(string)

		bob.send("2", "party.join", map[string]string{"party_id": pid})
		j, _ := bob.waitForMatch("party.join", 2*time.Second, "leader")
		expect("bob joined", strings.Contains(fmt.Sprint(j["data"]), "Alice"), fmt.Sprint(j["data"]))
		u, ok := alice.waitFor("party.update", 2*time.Second)
		expect("alice sees join push", ok, fmt.Sprint(u["data"]))

		// 3. Carol cannot join a full party.
		carol.send("3", "party.join", map[string]string{"party_id": pid})
		r, _ := carol.waitForMatch("party.join", 2*time.Second, "error")
		expect("full party rejected", strings.Contains(fmt.Sprint(r["data"]), "full"), fmt.Sprint(r["data"]))

		// 4. Leader leaves -> Bob becomes leader.
		alice.send("4", "party.leave", map[string]bool{})
		alice.waitFor("party.leave", 2*time.Second)
		u2, ok := bob.waitForMatch("party.update", 2*time.Second, "new_leader")
		expect("leader handover", ok && strings.Contains(fmt.Sprint(u2["data"]), "Bob"), fmt.Sprint(u2["data"]))

		// 5. Carol now joins the half-empty party.
		carol.send("5", "party.join", map[string]string{"party_id": pid})
		_, ok = carol.waitForMatch("party.join", 2*time.Second, "leader")
		expect("carol joins after slot freed", ok, "")

		alice.conn.Close()
		bob.conn.Close()
		carol.conn.Close()

	case "wallet":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		d := alice // wallet is per-user, one client is enough
		_ = d

		// Fresh balance.
		alice.send("1", "wallet.get", map[string]bool{})
		g, _ := alice.waitFor("wallet.get", 2*time.Second)
		before := fmt.Sprint(g["data"])

		// Credit +100.
		alice.send("2", "wallet.change", map[string]any{"amount": 100, "reason": "daily"})
		c1, _ := alice.waitForMatch("wallet.change", 2*time.Second, "coins")
		expect("credit +100", ok2(c1), fmt.Sprint(c1["data"]))

		// Debit -30.
		alice.send("3", "wallet.change", map[string]any{"amount": -30, "reason": "shop"})
		c2, _ := alice.waitForMatch("wallet.change", 2*time.Second, "coins")
		expect("debit -30", ok2(c2), fmt.Sprint(c2["data"]))

		// Overdraft rejected.
		alice.send("4", "wallet.change", map[string]any{"amount": -999999, "reason": "greed"})
		r, _ := alice.waitForMatch("wallet.change", 2*time.Second, "error")
		expect("overdraft rejected", strings.Contains(fmt.Sprint(r["data"]), "insufficient"), fmt.Sprint(r["data"]))

		// History has 2 entries.
		alice.send("5", "wallet.history", map[string]int{"limit": 10})
		h, _ := alice.waitFor("wallet.history", 2*time.Second)
		expect("history entries", strings.Contains(fmt.Sprint(h["data"]), "daily") && strings.Contains(fmt.Sprint(h["data"]), "shop"), before+" -> "+fmt.Sprint(h["data"]))

		alice.conn.Close()

	case "tournament":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		bob := dial("Bob", auth("dev-bob", "Bob"))

		alice.send("1", "tournament.create", map[string]any{"id": "weekly_sprint", "sort_order": 1, "operator": 0, "duration_sec": 3600, "max_attempts": 2})
		c, ok := alice.waitFor("tournament.create", 2*time.Second)
		expect("tournament created", ok, fmt.Sprint(c["data"]))

		// Attempts: Alice writes twice (best kept), third rejected.
		alice.send("2", "tournament.write", map[string]any{"id": "weekly_sprint", "score": 50})
		w1, _ := alice.waitForMatch("tournament.write", 2*time.Second, "score")
		expect("attempt 1", ok2(w1), fmt.Sprint(w1["data"]))
		alice.send("3", "tournament.write", map[string]any{"id": "weekly_sprint", "score": 70})
		w2, _ := alice.waitForMatch("tournament.write", 2*time.Second, "score")
		expect("attempt 2 (best=70)", strings.Contains(fmt.Sprint(w2["data"]), "score:70"), fmt.Sprint(w2["data"]))
		alice.send("4", "tournament.write", map[string]any{"id": "weekly_sprint", "score": 80})
		w3, _ := alice.waitForMatch("tournament.write", 2*time.Second, "error")
		expect("attempt 3 rejected (limit)", strings.Contains(fmt.Sprint(w3["data"]), "no attempts left"), fmt.Sprint(w3["data"]))

		// Bob joins and writes.
		bob.send("5", "tournament.write", map[string]any{"id": "weekly_sprint", "score": 60})
		bob.waitFor("tournament.write", 2*time.Second)

		// Standings.
		bob.send("6", "tournament.list", map[string]any{"id": "weekly_sprint", "limit": 5})
		l, _ := bob.waitFor("tournament.list", 2*time.Second)
		expect("standings Alice rank1", strings.Contains(fmt.Sprint(l["data"]), "rank:1") && strings.Contains(fmt.Sprint(l["data"]), "Alice"), fmt.Sprint(l["data"]))

		alice.conn.Close()
		bob.conn.Close()

	case "hooks":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		bob := dial("Bob", auth("dev-bob", "Bob"))

		alice.send("1", "chat.join", map[string]string{"channel": "mod"})
		alice.waitFor("chat.join", 2*time.Second)
		bob.send("2", "chat.join", map[string]string{"channel": "mod"})
		bob.waitFor("chat.join", 2*time.Second)

		// 1. Clean message passes the moderation before-hook.
		alice.send("3", "chat.send", map[string]string{"channel": "mod", "text": "good race!"})
		alice.waitFor("chat.send", 2*time.Second) // drain own ack
		_, ok := bob.waitFor("chat.message", 2*time.Second)
		expect("clean message passes hook", ok, "")

		// 2. Banned word -> before-hook rejects, Bob receives nothing.
		alice.send("4", "chat.send", map[string]string{"channel": "mod", "text": "buy my spam here"})
		r, _ := alice.waitForMatch("chat.send", 2*time.Second, "error")
		expect("hook rejects spam", strings.Contains(fmt.Sprint(r["data"]), "rejected by runtime hook"), fmt.Sprint(r["data"]))
		_, ok = bob.waitFor("chat.message", 600*time.Millisecond)
		expect("bob got nothing", !ok, "")

		// 3. Game-code RPC op.
		alice.send("5", "daily_reward", map[string]bool{})
		rw, ok := alice.waitFor("daily_reward", 2*time.Second)
		expect("RPC daily_reward works", ok && strings.Contains(fmt.Sprint(rw["data"]), "coins"), fmt.Sprint(rw["data"]))

		// 4. RPC that pushes a live notification back to the caller.
		alice.send("6", "ping_me", map[string]bool{})
		n, ok := alice.waitFor("notification", 2*time.Second)
		expect("RPC pushed notification", ok && strings.Contains(fmt.Sprint(n["data"]), "pong from RPC"), fmt.Sprint(n["data"]))

		alice.conn.Close()
		bob.conn.Close()

	case "groups":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		bob := dial("Bob", auth("dev-bob", "Bob"))

		// 1. Alice creates a closed clan.
		alice.send("1", "group.create", map[string]any{"name": "BullRiders", "description": "race clan", "open": false, "max_count": 10})
		c, ok := alice.waitFor("group.create", 2*time.Second)
		expect("group created", ok, fmt.Sprint(c["data"]))
		gid, _ := c["data"].(map[string]any)["group_id"].(string)

		// 2. Bob joins closed group -> join_request(3), edge_count unchanged.
		bob.send("2", "group.join", map[string]string{"group_id": gid})
		j, _ := bob.waitFor("group.join", 2*time.Second)
		expect("bob join_request(3)", strings.Contains(fmt.Sprint(j["data"]), "state:3"), fmt.Sprint(j["data"]))

		// 3. Alice (superadmin) accepts Bob -> member(2).
		alice.send("3", "group.accept", map[string]string{"group_id": gid, "username": "Bob"})
		a, _ := alice.waitFor("group.accept", 2*time.Second)
		expect("alice accepts bob", strings.Contains(fmt.Sprint(a["data"]), "accepted"), fmt.Sprint(a["data"]))

		// 4. Members list shows both with states.
		bob.send("4", "group.members", map[string]string{"group_id": gid})
		m, _ := bob.waitFor("group.members", 2*time.Second)
		expect("members: superadmin+member", strings.Contains(fmt.Sprint(m["data"]), "state:0") && strings.Contains(fmt.Sprint(m["data"]), "state:2"), fmt.Sprint(m["data"]))

		// 5. Bob can't accept (not admin).
		carol := dial("Carol", auth("dev-carol", "Carol"))
		carol.send("5", "group.join", map[string]string{"group_id": gid})
		carol.waitFor("group.join", 2*time.Second)
		bob.send("6", "group.accept", map[string]string{"group_id": gid, "username": "Carol"})
		r, _ := bob.waitFor("group.accept", 2*time.Second)
		expect("bob not admin -> rejected", strings.Contains(fmt.Sprint(r["data"]), "admin rights required"), fmt.Sprint(r["data"]))

		// 6. group.list search.
		carol.send("7", "group.list", map[string]string{"name": "bull"})
		l, _ := carol.waitFor("group.list", 2*time.Second)
		expect("search finds BullRiders", strings.Contains(fmt.Sprint(l["data"]), "BullRiders"), fmt.Sprint(l["data"]))

		alice.conn.Close()
		bob.conn.Close()
		carol.conn.Close()

	case "friends":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		bob := dial("Bob", auth("dev-bob", "Bob"))
		carol := dial("Carol", auth("dev-carol", "Carol"))

		// 1. Alice invites Bob: Alice sees invite_sent(1), Bob gets notified.
		alice.send("1", "friend.add", map[string]string{"username": "Bob"})
		_, ok := bob.waitFor("notification", 2*time.Second)
		expect("bob notified of invite", ok, "")

		alice.send("2", "friend.list", map[string]int{})
		l1, _ := alice.waitFor("friend.list", 2*time.Second)
		expect("alice sees invite_sent(1)", strings.Contains(fmt.Sprint(l1["data"]), "state\":1") || strings.Contains(fmt.Sprint(l1["data"]), "state:1"), fmt.Sprint(l1["data"]))

		// 2. Bob adds Alice back: mutual friends(0).
		bob.send("3", "friend.add", map[string]string{"username": "Alice"})
		r, _ := bob.waitFor("friend.add", 2*time.Second)
		expect("bob accept -> friend(0)", strings.Contains(fmt.Sprint(r["data"]), "state\":0") || strings.Contains(fmt.Sprint(r["data"]), "state:0"), fmt.Sprint(r["data"]))

		// 3. Online presence in friend list.
		alice.send("4", "friend.list", map[string]int{})
		l2, _ := alice.waitFor("friend.list", 2*time.Second)
		expect("alice sees bob friend(0)+online", strings.Contains(fmt.Sprint(l2["data"]), "online\":true") || strings.Contains(fmt.Sprint(l2["data"]), "online:true"), fmt.Sprint(l2["data"]))

		// 4. Carol blocks Alice; Alice can no longer add Carol.
		carol.send("5", "friend.block", map[string]string{"username": "Alice"})
		carol.waitFor("friend.block", 2*time.Second)
		alice.send("6", "friend.add", map[string]string{"username": "Carol"})
		r2, _ := alice.waitFor("friend.add", 2*time.Second)
		expect("alice blocked by carol", strings.Contains(fmt.Sprint(r2["data"]), "blocked"), fmt.Sprint(r2["data"]))

		// 5. Remove relationship.
		alice.send("7", "friend.remove", map[string]string{"username": "Bob"})
		alice.waitFor("friend.remove", 2*time.Second)
		alice.send("8", "friend.list", map[string]int{})
		l3, _ := alice.waitFor("friend.list", 2*time.Second)
		expect("bob removed from list", !strings.Contains(fmt.Sprint(l3["data"]), "Bob"), fmt.Sprint(l3["data"]))

		alice.conn.Close()
		bob.conn.Close()
		carol.conn.Close()

	case "notify":
		// Bob gets a PERSISTENT notification while offline.
		bobID := userID("dev-bob")
		alice := dial("Alice", auth("dev-alice", "Alice"))
		alice.send("1", "notification.send", map[string]any{
			"user_id": bobID, "subject": "friend request", "code": 10,
			"content": map[string]string{"from": "Alice"}, "persistent": true,
		})
		_, ok := alice.waitFor("notification.send", 2*time.Second)
		expect("send persisted", ok, "")

		// Bob connects later -> reads it from the list (was offline at send time).
		bob := dial("Bob", auth("dev-bob", "Bob"))
		bob.send("2", "notification.list", map[string]int{"limit": 5})
		list, ok := bob.waitFor("notification.list", 2*time.Second)
		found := ok && strings.Contains(fmt.Sprint(list["data"]), "friend request")
		expect("offline notification listed on reconnect", found, "")

		// Live-only notification while Bob IS online -> pushed instantly.
		alice.send("3", "notification.send", map[string]any{
			"user_id": bobID, "subject": "you are online!", "code": 0,
			"content": map[string]bool{}, "persistent": false,
		})
		push, ok := bob.waitFor("notification", 2*time.Second)
		expect("live push received", ok, fmt.Sprint(push["data"]))

		// Live-only is NOT persisted.
		bob.send("4", "notification.list", map[string]int{"limit": 10})
		list2, _ := bob.waitFor("notification.list", 2*time.Second)
		expect("live-only not persisted", !strings.Contains(fmt.Sprint(list2["data"]), "you are online"), "")
		alice.conn.Close()
		bob.conn.Close()

	case "race":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		cheater := dial("Cheater", auth("dev-cheater", "Cheater"))

		alice.send("1", "matchmaker.add", map[string]int{"min": 2, "max": 2})
		cheater.send("2", "matchmaker.add", map[string]int{"min": 2, "max": 2})
		mA, okA := alice.waitFor("match.found", 2*time.Second)
		mC, okC := cheater.waitFor("match.found", 2*time.Second)
		expect("race: both matched", okA && okC, "")
		matchID, _ := mA["data"].(map[string]any)["match_id"].(string)
		_ = mC

		start := time.Now()
		// Honest player: move 1.0 every 150 ms (6.6 u/s, under the 8 u/s cap).
		go func() {
			for range 200 {
				alice.send("m", "match.data", map[string]any{"match_id": matchID, "data": map[string]float64{"move": 1.0}})
				time.Sleep(150 * time.Millisecond)
			}
		}()
		// Cheater: move=999 as fast as possible (must still be capped to 8 u/s).
		go func() {
			for range 500 {
				cheater.send("x", "match.data", map[string]any{"match_id": matchID, "data": map[string]float64{"move": 999.0}})
				time.Sleep(10 * time.Millisecond)
			}
		}()

		// The earliest legit finish at 8 u/s over 100 units is 12.5 s.
		fin, ok := cheater.waitFor("race.finish", 30*time.Second)
		elapsed := time.Since(start)
		expect("race finished", ok, fmt.Sprint(fin["data"]))
		expect("speed cap enforced (>=12s)", elapsed >= 12*time.Second, elapsed.Round(time.Millisecond).String())
		alice.conn.Close()
		cheater.conn.Close()

	case "match":
		alice := dial("Alice", auth("dev-alice", "Alice"))
		bob := dial("Bob", auth("dev-bob", "Bob"))

		// 1. Alice queues alone: no match.
		alice.send("1", "matchmaker.add", map[string]int{"min": 2, "max": 2})
		_, ok := alice.waitFor("match.found", 600*time.Millisecond)
		expect("alice alone: no match", !ok, "")

		// 2. Bob queues: both matched, same channel.
		bob.send("2", "matchmaker.add", map[string]int{"min": 2, "max": 2})
		mA, okA := alice.waitFor("match.found", 2*time.Second)
		mB, okB := bob.waitFor("match.found", 2*time.Second)
		expect("both get match.found", okA && okB, "")
		chA, _ := mA["data"].(map[string]any)["channel"].(string)
		chB, _ := mB["data"].(map[string]any)["channel"].(string)
		expect("same match channel", chA != "" && chA == chB, chA)

		// 3. Chat over the auto-joined match channel.
		alice.send("3", "chat.send", map[string]string{"channel": chA, "text": "gl hf"})
		msg, ok := bob.waitFor("chat.message", 2*time.Second)
		expect("bob receives in-match chat", ok, fmt.Sprint(msg["data"]))

		// 4. Disconnect while queued: ticket must be cancelled.
		bob.send("4", "matchmaker.add", map[string]int{"min": 2, "max": 2})
		time.Sleep(100 * time.Millisecond)
		bob.conn.Close()
		time.Sleep(300 * time.Millisecond)
		alice.send("5", "matchmaker.add", map[string]int{"min": 2, "max": 2})
		_, ok = alice.waitFor("match.found", 800*time.Millisecond)
		expect("no match against stale ticket", !ok, "")

		alice.conn.Close()
	default:
		fmt.Println("unknown scenario:", scenario)
		os.Exit(1)
	}
}
