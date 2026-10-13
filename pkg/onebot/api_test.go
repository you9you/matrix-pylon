package onebot

import (
	"bytes"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
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
	go func() { _ = srv.Serve(ln) }()

	u, _ := url.Parse("ws://" + ln.Addr().String())
	wsConn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		_ = srv.Close()
		t.Fatalf("dial failed: %v", err)
	}

	svc := NewService(zerolog.Nop(), u.String(), 5*time.Second)
	client := NewClient(zerolog.Nop(), "test", "", svc)
	go client.StartLoop(wsConn)

	cleanup := func() {
		_ = wsConn.Close()
		client.Release()
		_ = srv.Close()
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

	// 参数为"包含 forward 节点的消息"的 message_id
	var msgs []Message
	var err error
	for range 50 {
		msgs, err = client.DownloadForwardMsg("999")
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

// TestDownloadForwardMsg_EmptyID 验证空 message_id 返回错误
func TestDownloadForwardMsg_EmptyID(t *testing.T) {
	svc := NewService(zerolog.Nop(), "ws://127.0.0.1:1", time.Second)
	client := NewClient(zerolog.Nop(), "test", "", svc)

	if _, err := client.DownloadForwardMsg(""); err == nil {
		t.Error("expected error for empty message_id, got nil")
	}
}

// TestDownloadMedia_PreferHTTP 验证 forward 媒体（preferHTTP=true）优先走 http
// 直链而非 get_image API：让 http 服务与 API 返回不同内容，preferHTTP 必须取
// http 内容（bug：forward 媒体 get_image 常 context deadline exceeded）。
// 若移除 preferHTTP 分支，本测试首条断言会拿到 apiPayload 而失败。
func TestDownloadMedia_PreferHTTP(t *testing.T) {
	httpPayload := []byte("FROM-HTTP-DIRECT-LINK")
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(httpPayload)
	}))
	defer httpSrv.Close()

	apiPayload := []byte("FROM-API-GET-IMAGE")
	// 模拟 OneBot WS：get_image 返回 base64(apiPayload)（与 http 内容不同）
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go func() {
			for {
				var req Request
				if err := conn.ReadJSON(&req); err != nil {
					return
				}
				resp := map[string]any{
					"status":  "ok",
					"retcode": 0,
					"echo":    req.Echo,
					"data": map[string]any{
						"file_name": "api.jpg",
						"base64":    base64.StdEncoding.EncodeToString(apiPayload),
					},
				}
				if err := conn.WriteJSON(resp); err != nil {
					return
				}
			}
		}()
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	u, _ := url.Parse("ws://" + ln.Addr().String())
	wsConn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = wsConn.Close() }()

	svc := NewService(zerolog.Nop(), u.String(), 5*time.Second)
	client := NewClient(zerolog.Nop(), "test", "", svc)
	defer client.Release()
	go client.StartLoop(wsConn)
	time.Sleep(100 * time.Millisecond) // 等 StartLoop 就绪，避免首个请求竞态

	seg := &ImageSegment{Segment: Segment{Type: string(Image), Data: map[string]any{ //nolint:modernize
		"file": "hash.jpg",
		"url":  httpSrv.URL,
	}}}

	// preferHTTP=true：必须取 http 直链内容，而非 get_image 返回的 apiPayload
	_, data, err := client.DownloadMedia(seg, true)
	if err != nil {
		t.Fatalf("preferHTTP download failed: %v", err)
	}
	if !bytes.Equal(data, httpPayload) {
		t.Errorf("preferHTTP data = %q, want %q", data, httpPayload)
	}

	// preferHTTP=false：走 get_image API，返回 apiPayload（证明两条路径内容不同）
	_, dataAPI, err := client.DownloadMedia(seg, false)
	if err != nil {
		t.Fatalf("api download failed: %v", err)
	}
	if !bytes.Equal(dataAPI, apiPayload) {
		t.Errorf("api data = %q, want %q", dataAPI, apiPayload)
	}
}
