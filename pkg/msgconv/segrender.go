package msgconv

import (
	"context"
	"fmt"
	"strings"

	"github.com/duo/matrix-pylon/pkg/onebot"
	"maunium.net/go/mautrix/bridgev2"
)

// renderSegment 渲染普通消息与合并转发消息中表现一致的 segment 类型
// （text/face/record/video/file/share/json 及未知类型）。
//
// 返回值 (part, handled)：
//   - handled=false：该类型由调用方自行处理（at/image/marketface/reply/forward），
//     不写入 b/mediaParts；
//   - part 非 nil：整段替换型（share/json），调用方直接采用；
//   - 其余：结果已写入 b 和/或 mediaParts。
func (mc *MessageConverter) renderSegment(ctx context.Context, client *onebot.Client, seg onebot.ISegment, b *strings.Builder, mediaParts *[]*bridgev2.ConvertedMessagePart) (part *bridgev2.ConvertedMessagePart, handled bool) {
	switch s := seg.(type) {
	case *onebot.AtSegment, *onebot.ImageSegment, *onebot.MarketFaceSegment,
		*onebot.ReplySegment, *onebot.ForwardSegment:
		// 调用方专属类型（普通消息与转发消息渲染不同），不在此处理
		return nil, false
	case *onebot.TextSegment:
		b.WriteString(convertOnebotEmoji(client, s.Content()))
	case *onebot.FaceSegment:
		b.WriteString(convertOnebotFace(client, s.ID()))
	case *onebot.RecordSegment:
		// BUG: NapCat get_record 下载可能失败（"Encoder not found"），
		// 见 pkg/onebot/protocol.go#NewGetRecordRequest
		*mediaParts = append(*mediaParts, mc.convertMediaMessage(ctx, s))
		b.WriteString("[Voice]")
	case *onebot.VideoSegment:
		*mediaParts = append(*mediaParts, mc.convertMediaMessage(ctx, s))
		b.WriteString("[Video]")
	case *onebot.FileSegment:
		*mediaParts = append(*mediaParts, mc.convertMediaMessage(ctx, s))
		b.WriteString("[File]")
	case *onebot.ShareSegment:
		return mc.convertShareMessage(s.Title(), s.Content(), s.URL()), true
	case *onebot.JSONSegment:
		return mc.convertJSONMessage(ctx, s), true
	default:
		fmt.Fprintf(b, "[%s]", seg.SegmentType())
	}
	return nil, true
}
