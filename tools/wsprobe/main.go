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
	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-c.in:
			if !ok {
				return nil, false
			}
			if msg["op"] == op {
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

func main() {
	scenario := "match"
	if len(os.Args) > 1 {
		scenario = os.Args[1]
	}

	switch scenario {
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
