package onebot

import (
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// TestStartLoop_MalformedEventNoCrash 验证 agent 发来的坏事件
// （text 段 data 缺 text 字段）不会因类型断言 panic 击穿整个进程：
// 修复前 handler goroutine 的 panic 会直接杀死测试进程（测试即失败），
// 修复后 StartLoop 在 goroutine 边界 recover 并丢弃该事件。
func TestStartLoop_MalformedEventNoCrash(t *testing.T) {
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// 推送一条坏事件：text 段的 data 缺 text 字段
		_ = conn.WriteJSON(map[string]any{
			"post_type":    "message",
			"message_type": "group",
			"message_id":   "1",
			"group_id":     "100",
			"user_id":      "200",
			"time":         123,
			"message": []any{
				map[string]any{"type": "text", "data": map[string]any{}},
			},
		})
		time.Sleep(time.Second)
		_ = conn.Close()
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	u, _ := url.Parse("ws://" + ln.Addr().String())
	wsConn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { _ = wsConn.Close() })

	svc := NewService(zerolog.Nop(), u.String(), 5*time.Second)
	client := NewClient(zerolog.Nop(), "test", "", svc)
	arrived := make(chan struct{})
	client.SetEventHandler(func(evt IEvent) {
		close(arrived)
		msg := evt.(*Message)
		for _, s := range msg.Message.([]ISegment) {
			if ts, ok := s.(*TextSegment); ok {
				_ = ts.Content() // 修复前在此 panic
			}
		}
	})
	go client.StartLoop(wsConn)

	select {
	case <-arrived:
		// 事件到达 handler 且进程仍存活：panic 被 recover
	case <-time.After(2 * time.Second):
		t.Fatal("malformed event did not reach the handler")
	}
}
