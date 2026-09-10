package msgconv

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/duo/matrix-pylon/pkg/ids"
	"github.com/duo/matrix-pylon/pkg/onebot"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

// flattenForward 将嵌套 forward 展平为扁平消息列表（保持顺序）
//
// Onebot 事件中的 forward 段往往只携带最外层 content（甚至完全为空，
// 如 NapCat 的 message_sent 回显），嵌套 forward 的真实内容需要通过
// get_forward_msg 接口按 id 下载。否则会漏掉嵌套消息，
// 导致 thread root 显示的条数仅为最外层数量。
func flattenForward(ctx context.Context, client *onebot.Client, data []onebot.Message) ([]onebot.Message, error) {
	result := make([]onebot.Message, 0, len(data))
	log := zerolog.Ctx(ctx)

	// 同一 id 的转发内容只下载一次，避免嵌套层重复请求
	downloadCache := make(map[string][]onebot.Message)

	// fetchForwardContent 获取转发段的完整消息内容：
	// 优先使用事件内联的 content，为空时回退到 get_forward_msg 接口下载
	var fetchForwardContent func(*onebot.ForwardSegment) ([]onebot.Message, error)
	fetchForwardContent = func(f *onebot.ForwardSegment) ([]onebot.Message, error) {
		if content, err := f.Content(); err == nil && len(content) > 0 {
			return content, nil
		}

		id := f.ID()
		if id == "" {
			return nil, fmt.Errorf("forward segment has no id and empty content")
		}

		if cached, ok := downloadCache[id]; ok {
			return cached, nil
		}

		content, err := client.DownloadForwardMsg(f)
		if err != nil {
			return nil, err
		}

		downloadCache[id] = content
		return content, nil
	}

	var walk func(msgs []onebot.Message) error
	walk = func(msgs []onebot.Message) error {
		for _, msg := range msgs {
			segs, ok := msg.Message.([]any)
			if !ok {
				return fmt.Errorf("failed msg.Message as []any")
			}
			segments := onebot.GenerateSegments(segs)
			hasForward := false
			var inner []onebot.Message
			for _, s := range segments {
				f, ok := s.(*onebot.ForwardSegment)
				if !ok {
					continue
				}
				hasForward = true

				content, err := fetchForwardContent(f)
				if err != nil {
					// 下载失败不中断整体转换：用占位消息替代，保留其余消息
					log.Warn().Err(err).Str("forward_id", f.ID()).Msg("Failed to fetch forward content, using placeholder")
					inner = append(inner, placeholderForwardMessage(f.ID()))
					continue
				}
				inner = append(inner, content...)
			}
			if hasForward {
				// 保持原语义：msg 内含 forward 时本层不产生 part，展开内部消息
				if err := walk(inner); err != nil {
					return err
				}
			} else {
				result = append(result, msg)
			}
		}
		return nil
	}
	if err := walk(data); err != nil {
		return nil, err
	}
	return result, nil
}

// placeholderForwardMessage 构造转发内容加载失败时的占位消息
func placeholderForwardMessage(forwardID string) onebot.Message {
	text := "[Chat History] 嵌套转发消息加载失败"
	if forwardID != "" {
		text = fmt.Sprintf("%s (id: %s)", text, forwardID)
	}
	return onebot.Message{
		Sender: onebot.Sender{Nickname: "unknown"},
		Message: []any{
			map[string]any{"type": "text", "data": map[string]any{"text": text}},
		},
	}
}

// 单个消息处理
func (mc *MessageConverter) convertMessageItem(ctx context.Context, client *onebot.Client,
	local *[]*bridgev2.ConvertedMessagePart, msg onebot.Message, forwardId, i int) error {

	var part *bridgev2.ConvertedMessagePart
	mediaParts := make([]*bridgev2.ConvertedMessagePart, 0)
	var contentBuilder strings.Builder

	// 发送者信息（昵称/UserID 可能包含 markdown 语法，需转义防止被渲染为链接/HTML）
	forwardInfo := fmt.Sprintf("[Forward]%s(%s)\n",
		format.EscapeMarkdown(msg.Sender.Nickname), format.EscapeMarkdown(msg.Sender.UserID))
	fmt.Fprint(&contentBuilder, forwardInfo)
	var hasImage = false

	msgs, ok := msg.Message.([]any)
	if !ok {
		return fmt.Errorf("failed msg.Message as []any")
	}

	// 需要将 []any 转换为 []ISegment
	segments := onebot.GenerateSegments(msgs)
	for _, s := range segments {
		switch v := s.(type) {
		case *onebot.TextSegment:
			fmt.Fprint(&contentBuilder, convertOnebotEmoji(client, v.Content()))
		case *onebot.FaceSegment:
			fmt.Fprint(&contentBuilder, convertOnebotFace(client, v.ID()))
		case *onebot.AtSegment:
			target := v.Target()
			if target == "all" {
				target = "room" // Matrix's mention all
			}
			fmt.Fprintf(&contentBuilder, "@%s", target)
		case *onebot.ImageSegment:
			hasImage = true
			p := mc.convertMediaMessage(ctx, v)
			mediaParts = append(mediaParts, p)

			if p.Content.URL != "" {
				filename := p.Content.FileName
				if filename == "" {
					filename = "image"
				}
				fmt.Fprintf(&contentBuilder, "![%s](%s)\n", filename, p.Content.URL)
			} else {
				// 图片上传失败，显示占位符而不是空链接
				zerolog.Ctx(ctx).Warn().Msg("Image upload failed in forward, using placeholder")
				fmt.Fprint(&contentBuilder, "[图片-上传失败]\n")
			}
		case *onebot.MarketFaceSegment:
			p := mc.convertMediaMessage(ctx, v)
			mediaParts = append(mediaParts, p)

			if p.Content.URL != "" {
				filename := p.Content.FileName
				if filename == "" {
					filename = "image"
				}
				fmt.Fprintf(&contentBuilder, "![%s](%s)\n", filename, p.Content.URL)
			} else {
				zerolog.Ctx(ctx).Warn().Msg("MarketFace upload failed in forward, using placeholder")
				fmt.Fprint(&contentBuilder, "[表情-上传失败]\n")
			}
		case *onebot.RecordSegment:

			//BUG: failed to download attachment: failed to download media: &{Segment:{Type:record Data:map[file:<REMOVE>.amr file_size:31028 path:/app/.config/QQ/nt_qq_<REMOVE>/nt_data/Ptt/2026-07/Ori/<REMOVE>.amr url:https://multimedia.nt.qq.com.cn/download?appid=1402&fileid=<REMOVE>&format=amr&rkey=<REMOVE>]}}
			// pkg/onebot/protocol.go#NewGetRecordRequest

			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[Voice]")
		case *onebot.VideoSegment:
			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[Video]")
		case *onebot.FileSegment:
			mediaParts = append(mediaParts, mc.convertMediaMessage(ctx, v))
			fmt.Fprint(&contentBuilder, "[File]")
		case *onebot.ReplySegment:
			// cm.ReplyTo = &networkid.MessageOptionalPartID{
			// 	MessageID: ids.MakeMessageID(ids.GetPeerID(&msg), v.ID()),
			// }
			//TODO
		case *onebot.ForwardSegment:
			// 递归实现显示合并消息(已由 flattenForward 处理，不再需要)
			// fmt.Fprint(&contentBuilder, "[Chat History]")
		case *onebot.ShareSegment:
			part = mc.convertShareMessage(v.Title(), v.Content(), v.URL())
		case *onebot.JSONSegment:
			part = mc.convertJSONMessage(ctx, v)
		default:
			fmt.Fprintf(&contentBuilder, "[%s]", v.SegmentType())
		}
	}

	if part == nil {
		if !hasImage && len(mediaParts) == 1 {
			// 单个 media 且 不是图片(可以直接输出，不需要 markdown 渲染)

			*local = append(*local, &bridgev2.ConvertedMessagePart{
				ID:   networkid.PartID(fmt.Sprintf("%d-%d-0", forwardId, i)),
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType: event.MsgText,
					Body:    forwardInfo,
				},
			})

			// 文件
			part = mediaParts[0]
			part.ID = networkid.PartID(fmt.Sprintf("%d-%d-1", forwardId, i))
		} else if len(mediaParts) >= 1 { // mixed image and text
			// 需要 markdown 渲染

			// mediaParts仅用作判断
			// var imagesMarkdown strings.Builder
			// for _, part := range mediaParts {
			// 	fmt.Fprintf(&imagesMarkdown, "![%s](%s)\n", part.Content.FileName, part.Content.URL)
			// }
			content := contentBuilder.String()
			rendered := format.RenderMarkdown(content, true, false)

			part = &bridgev2.ConvertedMessagePart{
				ID:   networkid.PartID(fmt.Sprintf("%d-%d", forwardId, i)),
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Format:        event.FormatHTML,
					Body:          content,
					FormattedBody: rendered.FormattedBody,
				},
			}
		} else {
			// 无 media

			part = &bridgev2.ConvertedMessagePart{
				ID:   networkid.PartID(fmt.Sprintf("%d-%d", forwardId, i)),
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType: event.MsgText,
					Body:    contentBuilder.String(),
				},
			}
		}
	}

	*local = append(*local, part)
	return nil
}

// 限制并发数，避免打爆 onebot websocket
const forwardConcurrency = 8

// 展平 + 并发 + 按序合并 + m.thread 支持
func (mc *MessageConverter) convertForwardMessage(ctx context.Context,
	client *onebot.Client,
	portal *bridgev2.Portal,
	intent bridgev2.MatrixAPI,
	data []onebot.Message,
	msg *onebot.Message,
	forwardId int,
) ([]*bridgev2.ConvertedMessagePart, error) {

	// 1. 展平嵌套 forward（matrix 不支持嵌套；
	//    嵌套 content 缺失时通过 get_forward_msg 接口下载）
	flatMsgs, err := flattenForward(ctx, client, data)
	if err != nil {
		return nil, err
	}

	if len(flatMsgs) == 0 {
		return nil, nil
	}

	// 2. 从 context 获取 intent 和 portal（或使用传入参数）
	if intent == nil {
		intent = getIntent(ctx)
	}
	if portal == nil {
		portal = getPortal(ctx)
	}
	if intent == nil || portal == nil {
		return nil, fmt.Errorf("intent or portal not available")
	}

	log := zerolog.Ctx(ctx).With().
		Str("portal_id", string(portal.ID)).
		Int("forward_count", len(flatMsgs)).
		Logger()

	// 3. 阶段1: 发送 "[Chat History]" 作为 thread root
	rootContent := &event.Content{
		Parsed: &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    fmt.Sprintf("[Chat History] 合并转发消息 (%d 条)", len(flatMsgs)),
		},
	}

	resp, err := intent.SendMessage(
		ctx,
		portal.MXID,
		event.EventMessage,
		rootContent,
		nil,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send [Chat History] root message")
		return nil, fmt.Errorf("failed to send chat history root: %w", err)
	}

	rootEventID := id.EventID(resp.EventID)
	log.Info().Str("root_event_id", string(rootEventID)).Msg("Sent [Chat History] as thread root")

	// 4. 手动插入 root message 到 DB（方案A：确保回复消息可被处理）
	// - 使用外层转发消息的真实网络 ID（peerID:msgID 格式），
	//   HandleMatrixMessage 中对 root 的回复能被 ParseMessageID 正确解析并路由到原始转发消息
	// - PartID 用 "0"：按 part_id ASC 排序时排在所有子 part（"0-0"、"0-1"…）之前，
	//   使 GetFirstPartByID / GetFirstThreadMessage 等查询返回 [Chat History] root 而不是第一条子消息
	// - Timestamp 用转发事件自身的时间（框架为子 part 写入的也是该时间），
	//   避免 time.Now() 晚于子 part 时间戳导致 GetFirstThreadMessage 取错 root
	rootNetworkID := ids.MakeMessageID(ids.GetPeerID(msg), msg.MessageID)
	// 确保 sender 的 ghost 行存在（message.sender_id 有外键约束）
	if _, err := mc.Bridge.GetGhostByID(ctx, networkid.UserID(msg.Sender.UserID)); err != nil {
		log.Warn().Err(err).Msg("Failed to ensure ghost row for root sender")
	}
	err = mc.Bridge.DB.Message.Insert(ctx, &database.Message{
		BridgeID:   mc.Bridge.ID,
		ID:         rootNetworkID,
		PartID:     networkid.PartID("0"),
		MXID:       rootEventID,
		Room:       portal.PortalKey,
		SenderID:   networkid.UserID(msg.Sender.UserID),
		SenderMXID: intent.GetMXID(),
		Timestamp:  time.UnixMilli(msg.Time * 1000),
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to insert chat history root to DB (non-fatal)")
	} else {
		log.Debug().Str("root_msg_id", string(rootNetworkID)).Msg("Inserted chat history root to DB")
	}

	// 5. 限制并发数
	sem := make(chan struct{}, forwardConcurrency)
	results := make([][]*bridgev2.ConvertedMessagePart, len(flatMsgs))

	var wg sync.WaitGroup
	var firstErr error
	var errLock sync.Mutex

	// 6. 并发处理每个扁平消息
	for i, msg := range flatMsgs {
		wg.Add(1)
		go func(i int, msg onebot.Message) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			local := make([]*bridgev2.ConvertedMessagePart, 0, 8)
			if err := mc.convertMessageItem(ctx, client, &local, msg, forwardId, i); err != nil {
				errLock.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errLock.Unlock()
				return
			}
			results[i] = local // 按索引写入，天然有序
		}(i, msg)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	// 7. 按 i 顺序合并，保证渲染顺序与 PartID 语义一致
	result := make([]*bridgev2.ConvertedMessagePart, 0, len(flatMsgs)*2)
	for i := range results {
		result = append(result, results[i]...)
	}

	// 8. 阶段2: 对每个 part 设置 m.thread，使其成为 root 的回复
	for _, part := range result {
		if part.Content == nil {
			continue
		}

		// 创建或获取 RelatesTo
		if part.Content.RelatesTo == nil {
			part.Content.RelatesTo = &event.RelatesTo{}
		}

		// SetThread(threadRootID, fallbackReplyToID)
		// 所有子消息都指向 [Chat History] root
		part.Content.RelatesTo.SetThread(rootEventID, rootEventID)
	}

	log.Info().Int("parts_count", len(result)).Msg("Forward message converted with m.thread")
	return result, nil
}
