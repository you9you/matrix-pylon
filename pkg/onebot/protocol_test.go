package onebot

import (
	"testing"
)

// TestGenerateSegments_Text 测试文本段解析
func TestGenerateSegments_Text(t *testing.T) {
	d := []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "hello"}},
	}
	segs := GenerateSegments(d)
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	ts, ok := segs[0].(*TextSegment)
	if !ok {
		t.Fatalf("expected *TextSegment, got %T", segs[0])
	}
	if ts.Content() != "hello" {
		t.Errorf("Content() = %q, want %q", ts.Content(), "hello")
	}
}

// TestGenerateSegments_Mixed 测试混合段解析
func TestGenerateSegments_Mixed(t *testing.T) {
	d := []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "hi"}},
		map[string]any{"type": "at", "data": map[string]any{"qq": "all"}},
		map[string]any{"type": "image", "data": map[string]any{"file": "f1", "url": "http://x"}},
		map[string]any{"type": "unknown_type", "data": map[string]any{}}, // 未知类型应被忽略
	}
	segs := GenerateSegments(d)
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}
	if _, ok := segs[0].(*TextSegment); !ok {
		t.Errorf("segs[0] = %T, want *TextSegment", segs[0])
	}
	at, ok := segs[1].(*AtSegment)
	if !ok {
		t.Fatalf("segs[1] = %T, want *AtSegment", segs[1])
	}
	if at.Target() != "all" {
		t.Errorf("Target() = %q, want %q", at.Target(), "all")
	}
	if _, ok := segs[2].(*ImageSegment); !ok {
		t.Errorf("segs[2] = %T, want *ImageSegment", segs[2])
	}
}

// TestGenerateSegments_ReplyAndForward 测试 reply/forward 段
func TestGenerateSegments_ReplyAndForward(t *testing.T) {
	d := []any{
		map[string]any{"type": "reply", "data": map[string]any{"id": "12345"}},
	}
	segs := GenerateSegments(d)
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	rs, ok := segs[0].(*ReplySegment)
	if !ok {
		t.Fatalf("expected *ReplySegment, got %T", segs[0])
	}
	if rs.ID() != "12345" {
		t.Errorf("ID() = %q, want %q", rs.ID(), "12345")
	}
}

// TestGenerateSegments_ForwardContent 测试 forward 段 Content 解析
func TestGenerateSegments_ForwardContent(t *testing.T) {
	d := []any{
		map[string]any{
			"type": "forward",
			"data": map[string]any{
				"id":      "fw-1",
				"content": []any{
					map[string]any{
						"user_id":    "200",
						"nickname":   "Bob",
						"message_id": "inner-1",
						"message": []any{
							map[string]any{"type": "text", "data": map[string]any{"text": "inner"}},
						},
					},
				},
			},
		},
	}
	segs := GenerateSegments(d)
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	fws, ok := segs[0].(*ForwardSegment)
	if !ok {
		t.Fatalf("expected *ForwardSegment, got %T", segs[0])
	}
	content, err := fws.Content()
	if err != nil {
		t.Fatalf("Content() error: %v", err)
	}
	if len(content) != 1 {
		t.Fatalf("expected 1 inner message, got %d", len(content))
	}
	if content[0].MessageID != "inner-1" {
		t.Errorf("inner MessageID = %q, want %q", content[0].MessageID, "inner-1")
	}
	// OneBot 11 转发内容中，发送者信息存放在顶层 user_id 字段，
	// 而不是嵌套的 sender 对象
	if content[0].UserID != "200" {
		t.Errorf("inner UserID = %q, want %q", content[0].UserID, "200")
	}
}

// TestGenerateSegments_Nil 测试空输入
func TestGenerateSegments_Nil(t *testing.T) {
	if segs := GenerateSegments(nil); len(segs) != 0 {
		t.Errorf("expected 0 segments, got %d", len(segs))
	}
}

// TestUnmarshalPayload_Message 测试消息 payload 分发
func TestUnmarshalPayload_Message(t *testing.T) {
	m := map[string]any{
		"post_type":   "message",
		"message_type": "private",
		"message_id":  "12345",
		"user_id":     "67890",
		"message": []any{
			map[string]any{"type": "text", "data": map[string]any{"text": "hi"}},
		},
	}
	p, err := UnmarshalPayload(m)
	if err != nil {
		t.Fatalf("UnmarshalPayload error: %v", err)
	}
	msg, ok := p.(*Message)
	if !ok {
		t.Fatalf("expected *Message, got %T", p)
	}
	// Message 字段应已被转换为 []onebot.ISegment
	segments, ok := msg.Message.([]ISegment)
	if !ok {
		t.Fatalf("msg.Message type = %T, want []ISegment", msg.Message)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	ts, ok := segments[0].(*TextSegment)
	if !ok {
		t.Fatalf("expected *TextSegment, got %T", segments[0])
	}
	if ts.Content() != "hi" {
		t.Errorf("Content() = %q, want %q", ts.Content(), "hi")
	}
}

// TestUnmarshalPayload_MessageSent 测试 message_sent 类型
func TestUnmarshalPayload_MessageSent(t *testing.T) {
	m := map[string]any{
		"post_type":   "message_sent",
		"message_type": "private",
		"message_id":  "99999",
		"user_id":     "111",
		"target_id":   "222",
		"message": []any{
			map[string]any{"type": "text", "data": map[string]any{"text": "sent"}},
		},
	}
	p, err := UnmarshalPayload(m)
	if err != nil {
		t.Fatalf("UnmarshalPayload error: %v", err)
	}
	msg, ok := p.(*Message)
	if !ok {
		t.Fatalf("expected *Message, got %T", p)
	}
	if msg.PostType != "message_sent" {
		t.Errorf("PostType = %q, want %q", msg.PostType, "message_sent")
	}
	if msg.TargetID != "222" {
		t.Errorf("TargetID = %q, want %q", msg.TargetID, "222")
	}
}

// TestUnmarshalPayload_GroupRecall 测试群消息撤回事件
func TestUnmarshalPayload_GroupRecall(t *testing.T) {
	m := map[string]any{
		"post_type":   "notice",
		"notice_type": "group_recall",
		"group_id":    "555",
		"user_id":     "111",
		"message_id":  "888",
	}
	p, err := UnmarshalPayload(m)
	if err != nil {
		t.Fatalf("UnmarshalPayload error: %v", err)
	}
	gc, ok := p.(*GroupRecall)
	if !ok {
		t.Fatalf("expected *GroupRecall, got %T", p)
	}
	if gc.GroupID != "555" {
		t.Errorf("GroupID = %q, want %q", gc.GroupID, "555")
	}
}

// TestUnmarshalPayload_Unsupported 测试不支持的 payload
func TestUnmarshalPayload_Unsupported(t *testing.T) {
	if _, err := UnmarshalPayload(map[string]any{}); err == nil {
		t.Error("expected error for empty payload")
	}
	if _, err := UnmarshalPayload(map[string]any{"post_type": "unknown"}); err == nil {
		t.Error("expected error for unknown post_type")
	}
}

// TestMessage_EventType 测试 EventType 判断
func TestMessage_EventType(t *testing.T) {
	priv := &Message{MessageType: "private"}
	if priv.EventType() != MessagePrivate {
		t.Errorf("private EventType = %q, want %q", priv.EventType(), MessagePrivate)
	}

	group := &Message{MessageType: "group"}
	if group.EventType() != MessageGroup {
		t.Errorf("group EventType = %q, want %q", group.EventType(), MessageGroup)
	}

	// 未知类型按 group 处理
	unknown := &Message{MessageType: ""}
	if unknown.EventType() != MessageGroup {
		t.Errorf("unknown EventType = %q, want %q", unknown.EventType(), MessageGroup)
	}
}

// TestNewText 测试 TextSegment 构造函数
func TestNewText(t *testing.T) {
	seg := NewText("hello")
	if seg.SegmentType() != Text {
		t.Errorf("SegmentType() = %q, want %q", seg.SegmentType(), Text)
	}
	if seg.Content() != "hello" {
		t.Errorf("Content() = %q, want %q", seg.Content(), "hello")
	}
}

// TestNewReply 测试 ReplySegment 构造函数
func TestNewReply(t *testing.T) {
	seg := NewReply("12345")
	if seg.SegmentType() != Reply {
		t.Errorf("SegmentType() = %q, want %q", seg.SegmentType(), Reply)
	}
	if seg.ID() != "12345" {
		t.Errorf("ID() = %q, want %q", seg.ID(), "12345")
	}
}

// TestNewImage 测试 ImageSegment 构造函数
func TestNewImage(t *testing.T) {
	seg := NewImage("file123", "test.jpg")
	if seg.SegmentType() != Image {
		t.Errorf("SegmentType() = %q, want %q", seg.SegmentType(), Image)
	}
	if seg.File() != "file123" {
		t.Errorf("File() = %q, want %q", seg.File(), "file123")
	}
}
