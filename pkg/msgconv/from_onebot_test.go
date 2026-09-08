package msgconv

import (
	"strings"
	"testing"

	"github.com/duo/matrix-pylon/pkg/onebot"
	"maunium.net/go/mautrix/event"
)

// msgFromRaw 与生产路径 unmarshalMessage 一致：用 GenerateSegments 生成 []onebot.ISegment
func msgFromRaw(msgID, userID, groupID, messageType, nickname string, raw any) onebot.Message {
	return onebot.Message{
		MessageID:   msgID,
		UserID:      userID,
		GroupID:     groupID,
		MessageType: messageType,
		Sender:      onebot.Sender{UserID: userID, Nickname: nickname},
		Message:     onebot.GenerateSegments(raw.([]any)),
	}
}

// TestOnebotToMatrix_TextOnly 测试纯文本消息转换
func TestOnebotToMatrix_TextOnly(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := msgFromRaw("12345", "67890", "", "private", "张三",
		[]any{map[string]any{"type": "text", "data": map[string]any{"text": "你好，世界"}}})

	cm := mc.OnebotToMatrix(ctx, client, testPortal(), nil, &msg)

	if cm == nil {
		t.Fatal("expected non-nil ConvertedMessage")
	}
	if len(cm.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(cm.Parts))
	}
	part := cm.Parts[0]
	if part.Type != event.EventMessage {
		t.Errorf("part.Type = %q, want %q", part.Type, event.EventMessage)
	}
	if part.Content.MsgType != event.MsgText {
		t.Errorf("MsgType = %q, want %q", part.Content.MsgType, event.MsgText)
	}
	if !strings.Contains(part.Content.Body, "你好，世界") {
		t.Errorf("Body should contain text, got %q", part.Content.Body)
	}
	if part.ID == "" {
		t.Error("part.ID should not be empty")
	}
}

// TestOnebotToMatrix_ReplySegment 测试私聊回复消息设置 ReplyTo
func TestOnebotToMatrix_ReplySegment(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := msgFromRaw("20001", "67890", "", "private", "张三",
		[]any{
			map[string]any{"type": "reply", "data": map[string]any{"id": "12345"}},
			map[string]any{"type": "text", "data": map[string]any{"text": "这是回复"}},
		})

	cm := mc.OnebotToMatrix(ctx, client, testPortal(), nil, &msg)

	if cm == nil {
		t.Fatal("expected non-nil ConvertedMessage")
	}
	if cm.ReplyTo == nil {
		t.Fatal("expected ReplyTo to be set")
	}
	// 私有消息 GetPeerID 返回 Sender.UserID
	wantID := "67890:12345"
	if string(cm.ReplyTo.MessageID) != wantID {
		t.Errorf("ReplyTo.MessageID = %q, want %q", cm.ReplyTo.MessageID, wantID)
	}
	if len(cm.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(cm.Parts))
	}
	if !strings.Contains(cm.Parts[0].Content.Body, "这是回复") {
		t.Errorf("Body should contain reply text, got %q", cm.Parts[0].Content.Body)
	}
}

// TestOnebotToMatrix_GroupReplySegment 测试群消息回复
func TestOnebotToMatrix_GroupReplySegment(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := msgFromRaw("30001", "111", "999", "group", "李四",
		[]any{
			map[string]any{"type": "reply", "data": map[string]any{"id": "25000"}},
			map[string]any{"type": "text", "data": map[string]any{"text": "群回复"}},
		})

	cm := mc.OnebotToMatrix(ctx, client, testPortal(), nil, &msg)

	if cm == nil {
		t.Fatal("expected non-nil ConvertedMessage")
	}
	if cm.ReplyTo == nil {
		t.Fatal("expected ReplyTo to be set")
	}
	// 群消息 GetPeerID 返回 GroupID
	wantID := "999:25000"
	if string(cm.ReplyTo.MessageID) != wantID {
		t.Errorf("ReplyTo.MessageID = %q, want %q", cm.ReplyTo.MessageID, wantID)
	}
}

// TestOnebotToMatrix_AtSegment 测试 @ 提及（all → room）
func TestOnebotToMatrix_AtSegment(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := msgFromRaw("40001", "111", "999", "group", "李四",
		// OneBot 11 at 段使用 qq 键（"all" 表示 @全体）
		[]any{
			map[string]any{"type": "at", "data": map[string]any{"qq": "all"}},
			map[string]any{"type": "text", "data": map[string]any{"text": " 大家好"}},
		})

	cm := mc.OnebotToMatrix(ctx, client, testPortal(), nil, &msg)

	if cm == nil || len(cm.Parts) == 0 {
		t.Fatal("expected non-empty ConvertedMessage")
	}
	if !strings.Contains(cm.Parts[0].Content.Body, "@room") {
		t.Errorf("Body should contain @room for 'all' at, got %q", cm.Parts[0].Content.Body)
	}
}

// TestOnebotToMatrix_InvalidSegments 测试无效 segments 的容错
func TestOnebotToMatrix_InvalidSegments(t *testing.T) {
	mc := testConverter()
	client := testClient()
	ctx := testCtx(client, testPortal(), nil)

	msg := onebot.Message{
		MessageID:   "50001",
		UserID:      "111",
		MessageType: "private",
		Sender:      onebot.Sender{UserID: "111"},
		Message:     "not-a-slice",
	}

	cm := mc.OnebotToMatrix(ctx, client, testPortal(), nil, &msg)

	if cm == nil || len(cm.Parts) != 1 {
		t.Fatal("expected 1 fallback part")
	}
	if !strings.Contains(cm.Parts[0].Content.Body, "[Error]") {
		t.Errorf("Body should contain error marker, got %q", cm.Parts[0].Content.Body)
	}
}

// TestConvertShareMessage_Basic 基本分享消息：标题加粗 + 描述 + 链接
func TestConvertShareMessage_Basic(t *testing.T) {
	mc := testConverter()
	part := mc.convertShareMessage("Test Title", "Test Desc", "https://example.com/page")

	if part.Type != event.EventMessage {
		t.Errorf("Type = %q, want %q", part.Type, event.EventMessage)
	}
	if part.Content.MsgType != event.MsgText {
		t.Errorf("MsgType = %q, want %q", part.Content.MsgType, event.MsgText)
	}
	if part.Content.Format != event.FormatHTML {
		t.Errorf("Format = %q, want %q", part.Content.Format, event.FormatHTML)
	}
	wantBody := "Test Title\n\nTest Desc\n\nhttps://example.com/page"
	if part.Content.Body != wantBody {
		t.Errorf("Body = %q, want %q", part.Content.Body, wantBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "Test Title") {
		t.Errorf("FormattedBody missing title: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "Test Desc") {
		t.Errorf("FormattedBody missing desc: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "https://example.com/page") {
		t.Errorf("FormattedBody missing url: %q", part.Content.FormattedBody)
	}
}

// TestConvertShareMessage_HTMLChars 特殊字符 < > & 必须在 HTML 中安全转义
func TestConvertShareMessage_HTMLChars(t *testing.T) {
	mc := testConverter()
	part := mc.convertShareMessage("5 < 6", "a & b > c", "https://ex.com/x?y=1&z=2")

	if !strings.Contains(part.Content.FormattedBody, "&lt;") {
		t.Errorf("'<' should be escaped as &lt;: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "&gt;") {
		t.Errorf("'>' should be escaped as &gt;: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "&amp;") {
		t.Errorf("'&' should be escaped as &amp;: %q", part.Content.FormattedBody)
	}
	// 必须不存在裸 HTML 标签（会被当作真实标签执行/渲染）
	if strings.Contains(part.Content.FormattedBody, "<script>") ||
		strings.Contains(part.Content.FormattedBody, "<iframe>") {
		t.Errorf("raw HTML tags leaked: %q", part.Content.FormattedBody)
	}
}

// TestConvertShareMessage_MarkdownChars 标题/描述含 markdown 符号时必须按字面量显示
func TestConvertShareMessage_MarkdownChars(t *testing.T) {
	mc := testConverter()
	// ** 和 * 若不转义会变成加粗斜体
	part := mc.convertShareMessage("a**b *c* d", "e_f [g](h) i", "https://ex.com")

	if !strings.Contains(part.Content.FormattedBody, "a**b") {
		t.Errorf("literal ** lost in title: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "a**b *c* d") ||
		!strings.Contains(part.Content.FormattedBody, "**b *c* d") ||
		!strings.Contains(part.Content.FormattedBody, "*c* d") {
		t.Errorf("literal *c* lost in title: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "e_f") {
		t.Errorf("literal _ lost in desc: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "[g](h)") {
		t.Errorf("literal link syntax lost in desc: %q", part.Content.FormattedBody)
	}
}

// TestConvertShareMessage_URLSpecialChars URL 含 markdown 保留字符 () [] 时链接不能断裂
func TestConvertShareMessage_URLSpecialChars(t *testing.T) {
	mc := testConverter()
	url := "https://ex.com/(foo)/bar[1].jpg?x=1&y=2"
	part := mc.convertShareMessage("t", "d", url)

	if !strings.Contains(part.Content.FormattedBody, "<a href=") {
		t.Fatalf("missing link tag: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.FormattedBody, "ex.com") {
		t.Errorf("url host lost: %q", part.Content.FormattedBody)
	}
	if !strings.Contains(part.Content.Body, url) {
		t.Errorf("plain body must keep full url, got %q", part.Content.Body)
	}
}

// TestConvertShareMessage_EmptyFields 空字段不得 panic
func TestConvertShareMessage_EmptyFields(t *testing.T) {
	mc := testConverter()
	if part := mc.convertShareMessage("", "", ""); part == nil || part.Content == nil {
		t.Fatal("expected non-nil part with empty inputs")
	}
}
