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

// startMockOnebot 启动一个模拟 Onebot 的 websocket 服务端。
//
// 对 get_forward_msg 请求返回预置的 data（通过 map 写入响应，
// 保证 data 字段的 JSON key 与真实协议一致）。
func startMockOnebot(t *testing.T, forwardData any) (*Client, *http.Server, func()) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade failed: %v", err)
			return
		}
		go func() {
			for {
				var req Request
				if err := conn.ReadJSON(&req); err != nil {
					return
				}
				if req.Action == string(GetForwardMsg) {
					resp := map[string]any{
						"status":  "ok",
						"retcode": 0,
						"data":    forwardData,
						"echo":    req.Echo,
					}
					if err := conn.WriteJSON(resp); err != nil {
						return
					}
				}
			}
		}()
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	u, _ := url.Parse("ws://" + ln.Addr().String())
	wsConn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial failed: %v", err)
	}

	svc := NewService(zerolog.Nop(), u.String(), 5*time.Second)
	client := NewClient(zerolog.Nop(), "test", "", svc)
	go client.StartLoop(wsConn)

	cleanup := func() {
		wsConn.Close()
		client.Release()
		srv.Close()
	}
	return client, srv, cleanup
}

// TestDownloadForwardMsg 验证 get_forward_msg 下载完整转发内容的链路
func TestDownloadForwardMsg(t *testing.T) {
	forwardData := map[string]any{
		"messages": []any{
			map[string]any{
				"user_id":    "100",
				"nickname":   "Alice",
				"message_id": "123",
				"sender": map[string]any{
					"user_id":  "100",
					"nickname": "Alice",
					"card":     "",
				},
				"message": []any{
					map[string]any{"type": "text", "data": map[string]any{"text": "hello"}},
				},
			},
			map[string]any{
				"user_id":    "200",
				"nickname":   "Bob",
				"message_id": "124",
				"message": []any{
					map[string]any{"type": "text", "data": map[string]any{"text": "world"}},
				},
			},
		},
	}
	client, _, cleanup := startMockOnebot(t, forwardData)
	defer cleanup()

	seg := &ForwardSegment{Segment{
		Type: string(Forward),
		Data: map[string]any{"id": "1234567890"},
	}}

	// 重试若干次，规避 StartLoop 尚未完成 updateConnection 的竞态
	var msgs []Message
	var err error
	for i := 0; i < 50; i++ {
		msgs, err = client.DownloadForwardMsg(seg)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("DownloadForwardMsg failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].MessageID != "123" || msgs[1].MessageID != "124" {
		t.Errorf("MessageID = %q, %q; want 123, 124", msgs[0].MessageID, msgs[1].MessageID)
	}
	if msgs[0].UserID != "100" {
		t.Errorf("UserID = %q, want 100", msgs[0].UserID)
	}
	if msgs[0].Sender.Nickname != "Alice" {
		t.Errorf("Sender.Nickname = %q, want Alice", msgs[0].Sender.Nickname)
	}
	// Message 必须保持原始 []any 形式（与 unmarshalMessage 一致），
	// 供 flattenForward / convertMessageItem 做 []any 类型断言
	if _, ok := msgs[0].Message.([]any); !ok {
		t.Errorf("Message should be []any, got %T", msgs[0].Message)
	}
}

// TestDownloadForwardMsg_NoID 验证无 id 的 forward 段不会 panic 并返回错误
func TestDownloadForwardMsg_NoID(t *testing.T) {
	svc := NewService(zerolog.Nop(), "ws://127.0.0.1:1", time.Second)
	client := NewClient(zerolog.Nop(), "test", "", svc)

	seg := &ForwardSegment{Segment{
		Type: string(Forward),
		Data: map[string]any{}, // 无 id
	}}
	if _, err := client.DownloadForwardMsg(seg); err == nil {
		t.Error("expected error for forward without id, got nil")
	}
}
