package msgconv

import (
	"testing"

	"github.com/duo/matrix-pylon/pkg/onebot"
)

// segmentsSummary 将 segment 列表格式化为可读字符串，便于断言与日志
func segmentsSummary(segments []onebot.ISegment) string {
	out := ""
	for _, s := range segments {
		switch v := s.(type) {
		case *onebot.TextSegment:
			out += "TEXT:" + v.Content() + ";"
		case *onebot.AtSegment:
			out += "AT:" + v.Target() + ";"
		default:
			out += "OTHER:" + string(v.SegmentType()) + ";"
		}
	}
	return out
}

// TestConstructMentionSegments_NoMentions 无 mention 时应只产生一个 text 段
func TestConstructMentionSegments_NoMentions(t *testing.T) {
	segments := constructMentionSegments("hello world", nil)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d (%s)", len(segments), segmentsSummary(segments))
	}
	if segments[0].SegmentType() != onebot.Text {
		t.Errorf("type = %q, want text", segments[0].SegmentType())
	}
}

// TestConstructMentionSegments_OIDWithDot 回归测试：
// OID 含 "." 等正则元字符时，mention 必须转成 at 段，而不是降级为纯文本。
func TestConstructMentionSegments_OIDWithDot(t *testing.T) {
	segments := constructMentionSegments("hello @wxid.abc123 world", []string{"wxid.abc123"})

	want := "TEXT:hello ;AT:wxid.abc123;TEXT: world;"
	if got := segmentsSummary(segments); got != want {
		t.Errorf("segments = %q, want %q", got, want)
	}

	for _, s := range segments {
		if at, ok := s.(*onebot.AtSegment); ok {
			if at.Target() != "wxid.abc123" {
				t.Errorf("At target = %q, want wxid.abc123", at.Target())
			}
		}
	}
}

// TestConstructMentionSegments_OIDWithSpecialChars OID 含括号/方括号时也应正确匹配
func TestConstructMentionSegments_OIDWithSpecialChars(t *testing.T) {
	oid := "user(test)[123]+"
	segments := constructMentionSegments("hi @user(test)[123]+ ok", []string{oid})

	want := "TEXT:hi ;AT:" + oid + ";TEXT: ok;"
	if got := segmentsSummary(segments); got != want {
		t.Errorf("segments = %q, want %q", got, want)
	}
}

// TestConstructMentionSegments_RoomMention @room 应转换为 all
func TestConstructMentionSegments_RoomMention(t *testing.T) {
	segments := constructMentionSegments("@room 通知", []string{"room"})
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d (%s)", len(segments), segmentsSummary(segments))
	}
	at, ok := segments[0].(*onebot.AtSegment)
	if !ok {
		t.Fatalf("first segment is not AtSegment: %s", segmentsSummary(segments))
	}
	if at.Target() != "all" {
		t.Errorf("At target = %q, want all", at.Target())
	}
}

// TestConstructMentionSegments_NoFalsePositive 防止正则误匹配：
// 文本中的 "@wxidXabc" 不应命中关键词 "@wxid.abc"（"." 转义后不能匹配任意字符）
func TestConstructMentionSegments_NoFalsePositive(t *testing.T) {
	segments := constructMentionSegments("hi @wxidXabc", []string{"wxid.abc"})

	for _, s := range segments {
		if _, ok := s.(*onebot.AtSegment); ok {
			t.Errorf("unexpected AtSegment produced for non-matching text: %s", segmentsSummary(segments))
		}
	}
}

// TestConstructMentionSegments_MultipleMentions 多个 mention 混合文本
func TestConstructMentionSegments_MultipleMentions(t *testing.T) {
	segments := constructMentionSegments("@a.b and @c.d(1) done", []string{"a.b", "c.d(1)"})

	want := "AT:a.b;TEXT: and ;AT:c.d(1);TEXT: done;"
	if got := segmentsSummary(segments); got != want {
		t.Errorf("segments = %q, want %q", got, want)
	}
}
