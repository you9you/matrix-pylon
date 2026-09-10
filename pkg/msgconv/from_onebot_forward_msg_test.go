package msgconv

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/duo/matrix-pylon/pkg/onebot"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

// msgText 构造一个仅含文本段的 OneBot 消息
func msgText(msgID, userID, nickname, text string) onebot.Message {
	return onebot.Message{
		MessageID:   msgID,
		Sender:      onebot.Sender{UserID: userID, Nickname: nickname},
		MessageType: "group",
		Message: []any{
			map[string]any{"type": "text", "data": map[string]any{"text": text}},
		},
	}
}

// msgImage 构造一个仅含图片段的 OneBot 消息
func msgImage(msgID, userID, nickname, file, url string) onebot.Message {
	return onebot.Message{
		MessageID:   msgID,
		Sender:      onebot.Sender{UserID: userID, Nickname: nickname},
		MessageType: "group",
		Message: []any{
			map[string]any{"type": "image", "data": map[string]any{"file": file, "url": url}},
		},
	}
}

// msgNestedForward 构造一个包含嵌套 forward 的消息
func msgNestedForward(msgID, userID, nickname string, inner []onebot.Message) onebot.Message {
	content := make([]any, 0, len(inner))
	for _, m := range inner {
		content = append(content, map[string]any{
			"user_id":    m.Sender.UserID,
			"nickname":   m.Sender.Nickname,
			"message_id": m.MessageID,
			"message":    m.Message,
		})
	}
	return onebot.Message{
		MessageID:   msgID,
		Sender:      onebot.Sender{UserID: userID, Nickname: nickname},
		MessageType: "group",
		Message: []any{
			map[string]any{"type": "forward", "data": map[string]any{"id": "fw-inner", "content": content}},
		},
	}
}

func TestFlattenForward_Flat(t *testing.T) {
	data := []onebot.Message{
		msgText("1", "100", "Alice", "hello"),
		msgText("2", "200", "Bob", "world"),
	}
	got, err := flattenForward(context.Background(), nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].MessageID != "1" || got[1].MessageID != "2" {
		t.Errorf("order not preserved: %q, %q", got[0].MessageID, got[1].MessageID)
	}
}

func TestFlattenForward_Nested(t *testing.T) {
	inner := []onebot.Message{
		msgText("10", "300", "Carol", "inner1"),
		msgText("11", "400", "Dave", "inner2"),
	}
	data := []onebot.Message{
		msgText("1", "100", "Alice", "before"),
		msgNestedForward("2", "200", "Bob", inner),
		msgText("3", "500", "Eve", "after"),
	}
	got, err := flattenForward(context.Background(), nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	want := []string{"1", "10", "11", "3"}
	for i, w := range want {
		if got[i].MessageID != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i].MessageID, w)
		}
	}
}

func TestFlattenForward_DeepNested(t *testing.T) {
	inner2 := []onebot.Message{msgText("100", "900", "Zoe", "deep")}
	inner1 := []onebot.Message{
		msgText("10", "300", "Carol", "mid"),
		msgNestedForward("11", "400", "Dave", inner2),
	}
	data := []onebot.Message{
		msgNestedForward("1", "100", "Alice", inner1),
	}
	got, err := flattenForward(context.Background(), nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 含 forward 的消息本身被展开替代：10（普通）+ 100（深层展开），11 被替代
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[1].MessageID != "100" {
		t.Errorf("expected deep message at index 1, got %q", got[1].MessageID)
	}
}

func TestFlattenForward_Empty(t *testing.T) {
	got, err := flattenForward(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestFlattenForward_InvalidMessageType(t *testing.T) {
	data := []onebot.Message{
		{
			MessageID: "1",
			Sender:    onebot.Sender{UserID: "1", Nickname: "Alice"},
			Message:   "not-a-slice",
		},
	}
	if _, err := flattenForward(context.Background(), nil, data); err == nil {
		t.Error("expected error for invalid message type, got nil")
	}
}

// TestFlattenForward_NestedEmptyContent_Placeholder
// 模拟 NapCat message_sent 回显场景：嵌套 forward 段只有 id、content 为空，
// get_forward_msg 下载失败（client 未连接）时应降级为占位消息，不中断整体转换
func TestFlattenForward_NestedEmptyContent_Placeholder(t *testing.T) {
	client := testClient() // 无 websocket 连接 → DownloadForwardMsg 必然失败

	nested := onebot.Message{
		MessageID:   "2",
		Sender:      onebot.Sender{UserID: "200", Nickname: "Bob"},
		MessageType: "group",
		Message: []any{
			map[string]any{
				"type": "forward",
				"data": map[string]any{
					"id":      "1234567890",
					"content": []any{},
				},
			},
		},
	}
	data := []onebot.Message{
		msgText("1", "100", "Alice", "before"),
		nested,
		msgText("3", "500", "Eve", "after"),
	}
	got, err := flattenForward(context.Background(), client, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// before + 占位消息 + after = 3
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %+v", len(got), got)
	}
	found := false
	for _, m := range got {
		msgs, ok := m.Message.([]any)
		if !ok {
			continue
		}
		for _, seg := range msgs {
			sm, ok := seg.(map[string]any)
			if !ok || sm["type"] != "text" {
				continue
			}
			d, ok := sm["data"].(map[string]any)
			if !ok {
				continue
			}
			text, _ := d["text"].(string)
			if strings.Contains(text, "加载失败") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("placeholder message not found in flattened result")
	}
}

func TestConvertMessageItem_TextOnly(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := msgText("1", "100", "Alice", "hello world")
	var local []*bridgev2.ConvertedMessagePart
	if err := mc.convertMessageItem(ctx, client, &local, msg, 0, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(local) != 1 {
		t.Fatalf("expected 1 part, got %d", len(local))
	}
	if local[0].ID != "0-0" {
		t.Errorf("part ID = %q, want %q", local[0].ID, "0-0")
	}
	if !strings.Contains(local[0].Content.Body, "hello world") {
		t.Errorf("body should contain 'hello world', got %q", local[0].Content.Body)
	}
	if !strings.Contains(local[0].Content.Body, "Alice") {
		t.Errorf("body should contain sender nickname 'Alice', got %q", local[0].Content.Body)
	}
}

func TestConvertMessageItem_ImageDownloadFails(t *testing.T) {
	mc := testConverter()
	client := testClient() // no connection → DownloadMedia fails
	ctx := testCtx(client, testPortal(), nil)

	msg := msgImage("1", "100", "Alice", "test.jpg", "")
	var local []*bridgev2.ConvertedMessagePart
	if err := mc.convertMessageItem(ctx, client, &local, msg, 0, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(local) != 1 {
		t.Fatalf("expected 1 part, got %d", len(local))
	}
	if !strings.Contains(local[0].Content.Body, "[图片-上传失败]") {
		t.Errorf("body should contain image failure placeholder, got %q", local[0].Content.Body)
	}
}

func TestConvertMessageItem_SingleNonImageMedia(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	// record segment: download will fail (no connection), but exercises the 2-part branch
	msg := onebot.Message{
		MessageID:   "1",
		Sender:      onebot.Sender{UserID: "100", Nickname: "Alice"},
		MessageType: "group",
		Message: []any{
			map[string]any{"type": "record", "data": map[string]any{"file": "test.amr"}},
		},
	}
	var local []*bridgev2.ConvertedMessagePart
	if err := mc.convertMessageItem(ctx, client, &local, msg, 0, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(local) != 2 {
		t.Fatalf("expected 2 parts (text info + media), got %d", len(local))
	}
	if local[0].ID != "0-0-0" {
		t.Errorf("part[0] ID = %q, want %q", local[0].ID, "0-0-0")
	}
	if local[1].ID != "0-0-1" {
		t.Errorf("part[1] ID = %q, want %q", local[1].ID, "0-0-1")
	}
	if !strings.Contains(local[0].Content.Body, "Alice") {
		t.Errorf("info part should contain sender nickname, got %q", local[0].Content.Body)
	}
}

func TestConvertMessageItem_ShareSegment(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := onebot.Message{
		MessageID:   "1",
		Sender:      onebot.Sender{UserID: "100", Nickname: "Alice"},
		MessageType: "group",
		Message: []any{
			map[string]any{"type": "share", "data": map[string]any{
				"title":   "Test Share",
				"content": "share desc",
				"url":     "https://example.com",
			}},
		},
	}
	var local []*bridgev2.ConvertedMessagePart
	if err := mc.convertMessageItem(ctx, client, &local, msg, 0, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(local) != 1 {
		t.Fatalf("expected 1 part, got %d", len(local))
	}
	if !strings.Contains(local[0].Content.Body, "Test Share") {
		t.Errorf("body should contain share title, got %q", local[0].Content.Body)
	}
}

// mockOnebotServer 启动一个模拟 Onebot 的 websocket 服务端，
// 对 get_forward_msg 请求返回预置的 data，并返回已连接的 Client
func mockOnebotServer(t *testing.T, forwardData any) *onebot.Client {
	t.Helper()

	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go func() {
			for {
				var req map[string]any
				if err := conn.ReadJSON(&req); err != nil {
					return
				}
				if req["action"] == "get_forward_msg" {
					resp := map[string]any{
						"status":  "ok",
						"retcode": 0,
						"data":    forwardData,
						"echo":    req["echo"],
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

	svc := onebot.NewService(zerolog.Nop(), u.String(), 5*time.Second)
	client := onebot.NewClient(zerolog.Nop(), "test", "", svc)
	go client.StartLoop(wsConn)

	t.Cleanup(func() {
		wsConn.Close()
		client.Release()
		srv.Close()
	})

	return client
}

// TestFlattenForward_NestedEmptyContent_DownloadsViaAPI
// 模拟 NapCat 的真实行为：嵌套 forward 段只有 id、content 为空，
// 必须通过 get_forward_msg 接口下载嵌套内容，展平后的数量要包含嵌套消息。
//
// mock 数据使用与真实 get_forward_msg 响应相同的结构（数字 ID、图片段、
// sender 对象），但不包含任何真实用户数据，验证真实数据格式能正确解码并展平。
func TestFlattenForward_NestedEmptyContent_DownloadsViaAPI(t *testing.T) {
	client := mockOnebotServer(t, map[string]any{
		"messages": []any{
			map[string]any{
				"self_id":      10000,
				"user_id":      20000,
				"time":         1789000001,
				"message_id":   30001,
				"message_type": "group",
				"sender": map[string]any{
					"user_id":  20000,
					"nickname": "UserA",
					"card":     "",
				},
				"group_id": 40000,
				"message": []any{
					map[string]any{
						"type": "image",
						"data": map[string]any{
							"summary":   "",
							"file":      "test-image-1.jpg",
							"sub_type":  0,
							"url":       "https://example.com/media/test-image-1.jpg",
							"file_size": "1024",
						},
					},
				},
				"message_format": "array",
				"post_type":      "message",
			},
			map[string]any{
				"self_id":      10000,
				"user_id":      20000,
				"time":         1789000002,
				"message_id":   30002,
				"message_type": "group",
				"sender": map[string]any{
					"user_id":  20000,
					"nickname": "UserB",
					"card":     "",
				},
				"group_id": 40000,
				"message": []any{
					map[string]any{"type": "text", "data": map[string]any{"text": "synthetic text"}},
				},
				"message_format": "array",
				"post_type":      "message",
			},
		},
	})

	// 外层节点：1 条普通文本 + 1 条嵌套 forward（content 为空，只有 id）
	data := []onebot.Message{
		msgText("1", "100", "Alice", "before"),
		{
			MessageID:   "2",
			Sender:      onebot.Sender{UserID: "200", Nickname: "Bob"},
			MessageType: "group",
			Message: []any{
				map[string]any{
					"type": "forward",
					"data": map[string]any{
						"id":      "fw-1",
						"content": []any{},
					},
				},
			},
		},
	}

	// 重试规避 StartLoop 尚未完成 updateConnection 的竞态
	var got []onebot.Message
	var lastErr error
	for i := 0; i < 50; i++ {
		var ferr error
		got, ferr = flattenForward(context.Background(), client, data)
		lastErr = ferr
		if ferr == nil && len(got) == 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("flattenForward failed: %v", lastErr)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 (outer 1 + nested 2), got %d", len(got))
	}
	// before + 嵌套下载的消息（数字 ID 应弱解码为字符串）
	if got[0].MessageID != "1" {
		t.Errorf("got[0].MessageID = %q, want 1", got[0].MessageID)
	}
	if got[1].MessageID != "30001" {
		t.Errorf("got[1].MessageID = %q, want 30001", got[1].MessageID)
	}
	if got[2].MessageID != "30002" {
		t.Errorf("got[2].MessageID = %q, want 30002", got[2].MessageID)
	}
	// 嵌套消息的 sender 对象应正确解码
	if got[1].Sender.Nickname != "UserA" {
		t.Errorf("got[1].Sender.Nickname = %q, want UserA", got[1].Sender.Nickname)
	}
	if got[1].Sender.UserID != "20000" {
		t.Errorf("got[1].Sender.UserID = %q, want 20000", got[1].Sender.UserID)
	}
	// 图片段应保持原始 []any，供 convertMessageItem 的 GenerateSegments 处理
	if _, ok := got[1].Message.([]any); !ok {
		t.Errorf("got[1].Message should be []any, got %T", got[1].Message)
	}
}
