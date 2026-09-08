package msgconv

import (
	"strings"
	"testing"

	"github.com/duo/matrix-pylon/pkg/onebot"
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
	got, err := flattenForward(data)
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
	got, err := flattenForward(data)
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
	got, err := flattenForward(data)
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
	got, err := flattenForward(nil)
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
	if _, err := flattenForward(data); err == nil {
		t.Error("expected error for invalid message type, got nil")
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
